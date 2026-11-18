// Package research is the adapter layer between LifeOS research and the
// outside world: the built-in search provider, page reading, and the user's
// connected sources (an HTTP API, an MCP server or a configured command).
//
// The adapter does no product thinking: it fetches, decodes and parses. What a
// page is worth, and whether a claim is a fact, is decided by the domain and
// the application, so the same pages always produce the same report.
package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

const (
	defaultTimeout      = 20 * time.Second
	defaultMaxPageBytes = 3 << 20 // 3 MiB
	defaultMaxCallBytes = 256 << 10
	// defaultUserAgent identifies LifeOS honestly; some sites serve a stub to
	// unknown agents, which is why the string is a normal browser-shaped one.
	defaultUserAgent = "Mozilla/5.0 (compatible; LifeOS/1.0; +personal-assistant)"
	// DefaultSearchURL is the free, key-less provider LifeOS falls back to.
	DefaultSearchURL = "https://html.duckduckgo.com/html/?q={query}"
)

// Options configures the client.
type Options struct {
	// SearchURL is a query template containing {query}. Empty keeps the
	// built-in provider; "off" disables web search entirely.
	SearchURL string
	// SearchFormat selects the parser: searxng or duckduckgo.
	SearchFormat string
	UserAgent    string
	Timeout      time.Duration
	MaxPageBytes int64
	// Client replaces the transport. It is used as-is only together with
	// AllowPrivateHosts, which is the explicit "my network is my own" switch;
	// otherwise every request goes through the guard whatever client is
	// configured.
	Client *http.Client
	// Resolver resolves host names; tests replace it to describe a network
	// that does not exist.
	Resolver Resolver
	// AllowPrivateHosts lets traffic reach loopback and private addresses. It
	// exists for tests and for a single-user install that deliberately uses
	// its own network; the default refuses them, because a page address comes
	// from a search result or from the model, not from the user. Service
	// addresses are refused either way.
	AllowPrivateHosts bool
}

// Client implements research.External.
type Client struct {
	injected     *http.Client
	resolver     Resolver
	searchURL    string
	searchFormat domainresearch.SearchFormat
	userAgent    string
	maxPageBytes int64
	timeout      time.Duration
	// allowPrivateHosts accepts non-public addresses; service addresses stay
	// refused.
	allowPrivateHosts bool
}

// NewClient builds the adapter.
func NewClient(options Options) *Client {
	// Only a client the caller actually supplied counts as injected: the
	// guard is the default for every request, and an injected transport is an
	// explicit "my network is my own" that comes with AllowPrivateHosts.
	client := options.Client
	if client == nil && !options.AllowPrivateHosts {
		timeout := options.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	searchURL := strings.TrimSpace(options.SearchURL)
	format := domainresearch.SearchFormat(strings.ToLower(strings.TrimSpace(options.SearchFormat)))
	if searchURL == "" {
		searchURL = DefaultSearchURL
	}
	if strings.EqualFold(searchURL, "off") || strings.EqualFold(searchURL, "none") {
		searchURL = ""
	}
	if format == "" {
		format = domainresearch.FormatDuckDuckGo
		if searchURL != DefaultSearchURL {
			// A custom endpoint is far more likely to be a SearxNG JSON API
			// than a second DuckDuckGo HTML mirror.
			format = domainresearch.FormatSearxng
		}
	}
	if format == domainresearch.FormatSearxng && strings.Contains(searchURL, "{query}") && !strings.Contains(searchURL, "format=") {
		searchURL = searchURL + "&format=json"
	}
	userAgent := options.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	maxPage := options.MaxPageBytes
	if maxPage <= 0 {
		maxPage = defaultMaxPageBytes
	}
	return &Client{
		injected:          client,
		resolver:          options.Resolver,
		searchURL:         searchURL,
		searchFormat:      format,
		userAgent:         userAgent,
		maxPageBytes:      maxPage,
		timeout:           options.Timeout,
		allowPrivateHosts: options.AllowPrivateHosts,
	}
}

// Search queries a search endpoint: the connection's own when one is given,
// the built-in provider otherwise.
func (c *Client) Search(ctx context.Context, connection *domainresearch.Connection, query string, limit int) ([]appresearch.SearchResult, error) {
	endpoint := c.searchURL
	format := c.searchFormat
	if connection != nil {
		endpoint = connection.Endpoint
		format = domainresearch.SearchFormat(connection.Format)
		if format == "" {
			format = c.searchFormat
		}
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, appresearch.ErrNoProvider
	}
	if strings.TrimSpace(query) == "" {
		return nil, appresearch.ErrEmptyQuestion
	}
	target, err := buildSearchURL(endpoint, query)
	if err != nil {
		return nil, err
	}
	// The search endpoint is the user's own choice and may live on the local
	// network; that allowance is for its own host only, and every redirect is
	// checked like any other address.
	body, contentType, err := c.getWith(ctx, c.guardedClient(policyUserSource, hostOf(target)), target)
	if err != nil {
		return nil, err
	}
	switch format {
	case domainresearch.FormatSearxng:
		return parseSearxng(body, limit)
	default:
		return parseDuckDuckGo(body, contentOrHTML(contentType), limit)
	}
}

// Fetch reads one page and returns its readable text.
func (c *Client) Fetch(ctx context.Context, rawURL string) (appresearch.Page, error) {
	target := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return appresearch.Page{}, fmt.Errorf("адрес страницы некорректен: %q", rawURL)
	}
	guard := publicHostGuard{policy: policyPublicPage, allowLocal: c.allowPrivateHosts, resolver: c.resolver}
	if err := guard.checkURL(ctx, parsed); err != nil {
		return appresearch.Page{}, err
	}
	client := c.guardedClient(policyPublicPage, "")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return appresearch.Page{}, err
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")
	request.Header.Set("Accept-Language", "ru,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		return appresearch.Page{}, fmt.Errorf("не удалось открыть %s: %w", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return appresearch.Page{}, fmt.Errorf("%s ответил %d", parsed.Host, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxPageBytes+1))
	if err != nil {
		return appresearch.Page{}, fmt.Errorf("не удалось прочитать %s: %w", target, err)
	}
	truncated := int64(len(body)) > c.maxPageBytes
	if truncated {
		body = body[:c.maxPageBytes]
	}
	title, text := readableContent(body, response.Header.Get("Content-Type"))
	if strings.TrimSpace(text) == "" {
		return appresearch.Page{}, fmt.Errorf("на странице %s нет читаемого текста", parsed.Host)
	}
	return appresearch.Page{
		URL:       response.Request.URL.String(),
		Title:     title,
		Text:      text,
		Truncated: truncated,
	}, nil
}

// Call talks to a connected source.
func (c *Client) Call(ctx context.Context, connection *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	if connection == nil {
		return appresearch.CallResult{}, appresearch.ErrInvalidCall
	}
	if connection.Kind != domainresearch.KindScript {
		// A source is the user's own choice of address and may live on their
		// network; a service address is not a service anyone connects on
		// purpose, and redirects away from the source are checked hop by hop.
		endpoint, err := url.Parse(strings.TrimSpace(connection.Endpoint))
		if err != nil {
			return appresearch.CallResult{}, appresearch.ErrInvalidCall
		}
		guard := publicHostGuard{
			policy:     policyUserSource,
			originHost: hostOf(connection.Endpoint),
			allowLocal: c.allowPrivateHosts,
			resolver:   c.resolver,
		}
		if err := guard.checkURL(ctx, endpoint); err != nil {
			return appresearch.CallResult{}, err
		}
	}
	switch connection.Kind {
	case domainresearch.KindHTTPAPI:
		return c.callHTTPAPI(ctx, connection, request)
	case domainresearch.KindMCP:
		return c.callMCP(ctx, connection, request)
	case domainresearch.KindScript:
		return c.callScript(ctx, connection, request)
	case domainresearch.KindWebSearch:
		results, err := c.Search(ctx, connection, request.Query, 10)
		if err != nil {
			return appresearch.CallResult{}, err
		}
		body, err := jsonString(results)
		if err != nil {
			return appresearch.CallResult{}, err
		}
		return appresearch.CallResult{Status: http.StatusOK, ContentType: "application/json", Body: body}, nil
	}
	return appresearch.CallResult{}, appresearch.ErrInvalidCall
}

// callHTTPAPI calls one path of a REST endpoint.
func (c *Client) callHTTPAPI(ctx context.Context, connection *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	target, err := joinEndpoint(connection.Endpoint, request.Operation)
	if err != nil {
		return appresearch.CallResult{}, err
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if strings.TrimSpace(request.Body) != "" {
		body = bytes.NewReader([]byte(request.Body))
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return appresearch.CallResult{}, err
	}
	httpRequest.Header.Set("User-Agent", c.userAgent)
	httpRequest.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	response, err := c.guardedClient(policyUserSource, hostOf(connection.Endpoint)).Do(httpRequest)
	if err != nil {
		return appresearch.CallResult{}, fmt.Errorf("источник недоступен: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, defaultMaxCallBytes))
	if err != nil {
		return appresearch.CallResult{}, fmt.Errorf("ответ источника не прочитан: %w", err)
	}
	result := appresearch.CallResult{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        string(payload),
		Duration:    time.Since(started),
	}
	if response.StatusCode >= 400 {
		return result, fmt.Errorf("источник ответил %d: %s", response.StatusCode, truncate(collapse(payload), 300))
	}
	return result, nil
}

// joinEndpoint appends an operation to a base URL. An absolute operation is
// accepted only on the same host, so a source cannot be used to reach
// somewhere else.
func joinEndpoint(endpoint, operation string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || base.Host == "" {
		return "", appresearch.ErrInvalidCall
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return base.String(), nil
	}
	if parsed, err := url.Parse(operation); err == nil && parsed.Host != "" {
		if !strings.EqualFold(parsed.Host, base.Host) {
			return "", fmt.Errorf("%w: адрес вне подключённого источника", appresearch.ErrInvalidCall)
		}
		return parsed.String(), nil
	}
	if !strings.HasPrefix(operation, "/") {
		operation = "/" + operation
	}
	base.Path = strings.TrimRight(base.Path, "/") + operation
	base.RawQuery = ""
	return base.String(), nil
}

// buildSearchURL fills the query template.
func buildSearchURL(endpoint, query string) (string, error) {
	escaped := url.QueryEscape(query)
	if strings.Contains(endpoint, "{query}") {
		return strings.ReplaceAll(endpoint, "{query}", escaped), nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("адрес поиска некорректен: %q", endpoint)
	}
	if parsed.RawQuery != "" {
		return endpoint + "&q=" + escaped, nil
	}
	return endpoint + "?q=" + escaped, nil
}

// getWith performs a GET with the guarded client of the caller's policy, which
// is how page reads, searches and source calls all carry the same check.
func (c *Client) getWith(ctx context.Context, client *http.Client, target string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.5")
	request.Header.Set("Accept-Language", "ru,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("поиск недоступен: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("поиск ответил %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxPageBytes))
	if err != nil {
		return nil, "", fmt.Errorf("ответ поиска не прочитан: %w", err)
	}
	return body, response.Header.Get("Content-Type"), nil
}

// hostOf returns the host of an address, for the guard's allowlist.
func hostOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func contentOrHTML(contentType string) string {
	if strings.TrimSpace(contentType) == "" {
		return "text/html"
	}
	return contentType
}

func jsonString(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func collapse(payload []byte) string {
	return strings.Join(strings.Fields(string(payload)), " ")
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
