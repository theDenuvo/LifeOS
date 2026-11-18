package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// newTestClient builds a client for tests: the fixtures live on loopback, which
// production page reading refuses on purpose.
func newTestClient(options Options) *Client {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	options.AllowPrivateHosts = true
	return NewClient(options)
}

func TestSearchParsesDuckDuckGoHTML(t *testing.T) {
	const markup = `<!DOCTYPE html><html><body>
	<div class="result results_links results_links_deep web-result">
		<h2 class="result__title">
			<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fgo1.24&amp;rut=abc">Go 1.24 Release Notes</a>
		</h2>
		<a class="result__snippet" href="#">Go 1.24 adds generic type aliases and a faster runtime.</a>
	</div>
	<div class="result results_links">
		<h2 class="result__title"><a class="result__a" href="https://example.org/post">A post</a></h2>
		<a class="result__snippet">Some snippet about the release.</a>
	</div>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			t.Errorf("search request must carry the query, got %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, markup)
	}))
	defer server.Close()

	client := newTestClient(Options{SearchURL: server.URL + "/?q={query}", SearchFormat: "duckduckgo"})
	results, err := client.Search(context.Background(), nil, "go 1.24", 5)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %+v", results)
	}
	if results[0].URL != "https://go.dev/doc/go1.24" {
		t.Fatalf("the redirect must be unwrapped, got %q", results[0].URL)
	}
	if results[0].Title != "Go 1.24 Release Notes" {
		t.Fatalf("title = %q", results[0].Title)
	}
	if !strings.Contains(results[0].Snippet, "generic type aliases") {
		t.Fatalf("snippet = %q", results[0].Snippet)
	}
	if results[1].URL != "https://example.org/post" {
		t.Fatalf("second url = %q", results[1].URL)
	}
}

func TestSearchParsesSearxngJSON(t *testing.T) {
	const payload = `{"results":[
		{"title":"Kafka","url":"https://kafka.apache.org/","content":"Apache Kafka is an event streaming platform."},
		{"title":"Docs","url":"https://kafka.apache.org/documentation/","snippet":"Documentation."}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	}))
	defer server.Close()

	connection := &domainresearch.Connection{
		ID: 1, Name: "Searx", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Format: domainresearch.FormatSearxng, Endpoint: server.URL + "/search?q={query}&format=json",
		Enabled: true, Trust: domainresearch.TrustHigh,
	}
	client := newTestClient(Options{})
	results, err := client.Search(context.Background(), connection, "kafka", 10)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 || results[0].Title != "Kafka" {
		t.Fatalf("results = %+v", results)
	}
	if !strings.Contains(results[0].Snippet, "event streaming") {
		t.Fatalf("snippet = %q", results[0].Snippet)
	}
}

func TestSearchWithoutEndpointReportsNoProvider(t *testing.T) {
	client := newTestClient(Options{SearchURL: "off"})
	if _, err := client.Search(context.Background(), nil, "q", 5); err != appresearch.ErrNoProvider {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestFetchReducesHTMLToReadableText(t *testing.T) {
	const markup = `<!DOCTYPE html><html><head>
		<title>Release notes — Go</title>
		<meta charset="utf-8">
		<style>body{color:red}</style>
		<script>var tracking = "do not read me";</script>
	</head><body>
		<nav><a href="/">Home</a><a href="/docs">Docs</a></nav>
		<main>
			<h1>Go 1.24</h1>
			<p>The release adds generic type aliases. They let a package name a parameterised type.</p>
			<p>The runtime also improves small-object allocation.</p>
		</main>
		<footer>© 2025</footer>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("a user agent must be sent")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, markup)
	}))
	defer server.Close()

	client := newTestClient(Options{})
	page, err := client.Fetch(context.Background(), server.URL+"/doc")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if page.Title != "Release notes — Go" {
		t.Fatalf("title = %q", page.Title)
	}
	for _, banned := range []string{"tracking", "color:red", "Home", "© 2025"} {
		if strings.Contains(page.Text, banned) {
			t.Fatalf("text must not contain %q: %q", banned, page.Text)
		}
	}
	if !strings.Contains(page.Text, "generic type aliases") {
		t.Fatalf("text lost the content: %q", page.Text)
	}
	if !strings.Contains(page.Text, "\n") {
		t.Fatalf("block elements must produce line breaks: %q", page.Text)
	}
}

func TestFetchDecodesWindows1251(t *testing.T) {
	// "Привет, мир" in windows-1251.
	body := []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2, 0x2C, 0x20, 0xEC, 0xE8, 0xF0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		w.Write(append([]byte("<html><body><p>"), append(body, []byte("</p></body></html>")...)...))
	}))
	defer server.Close()

	client := newTestClient(Options{})
	page, err := client.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !strings.Contains(page.Text, "Привет, мир") {
		t.Fatalf("windows-1251 was not decoded: %q", page.Text)
	}
}

func TestFetchRejectsNonHTTPURLs(t *testing.T) {
	client := newTestClient(Options{})
	if _, err := client.Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("a file URL is not a web page")
	}
}

func TestHTTPAPICallJoinsThePathAndReportsErrors(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: server.URL + "/v1", Enabled: true,
	}
	result, err := client.Call(context.Background(), connection, appresearch.CallRequest{
		Operation: "/repos/owner/name", Method: "POST", Body: `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if gotPath != "/v1/repos/owner/name" || gotMethod != "POST" || gotBody != `{"a":1}` {
		t.Fatalf("request = %s %s %q", gotMethod, gotPath, gotBody)
	}
	if result.Status != 200 || !strings.Contains(result.Body, "ok") {
		t.Fatalf("result = %+v", result)
	}

	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{
		Operation: "https://evil.example.com/steal",
	}); err == nil {
		t.Fatal("an operation on another host must be refused")
	}
}

func TestHTTPAPIReportsErrorStatusWithDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: server.URL, Enabled: true,
	}
	_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "/x"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected the status in the error, got %v", err)
	}
}

func TestMCPListsToolsAndCallsOneOverEventStream(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		decodeErr := decodeRequestBody(r, &message)
		if decodeErr != nil {
			t.Errorf("bad body: %v", decodeErr)
		}
		calls = append(calls, message.Method+message.Params.Name)
		switch message.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-1")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"search_repos","annotations":{"readOnlyHint":true}}]}}`)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n\n")
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "MCP", Kind: domainresearch.KindMCP, Mode: domainresearch.ModeRead,
		Endpoint: server.URL + "/mcp", Enabled: true,
		AllowedTools: []string{"search_repos"},
	}
	list, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "tools/list"})
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	if !strings.Contains(list.Body, "search_repos") {
		t.Fatalf("body = %q", list.Body)
	}

	result, err := client.Call(context.Background(), connection, appresearch.CallRequest{
		Operation: "search_repos", Body: `{"query":"lifeos"}`,
	})
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}
	if !strings.Contains(result.Body, `"ok"`) {
		t.Fatalf("body = %q", result.Body)
	}
	joined := strings.Join(calls, ",")
	if !strings.Contains(joined, "initialize") || !strings.Contains(joined, "tools/callsearch_repos") {
		t.Fatalf("calls = %v", calls)
	}
}

func TestMCPSurfacesProtocolErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Method string `json:"method"`
		}
		_ = decodeRequestBody(r, &message)
		w.Header().Set("Content-Type", "application/json")
		if message.Method == "initialize" {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"error":{"code":-32601,"message":"unknown tool"}}`)
	}))
	defer server.Close()

	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "MCP", Kind: domainresearch.KindMCP, Mode: domainresearch.ModeRead,
		Endpoint: server.URL, Enabled: true,
		AllowedTools: []string{"nope"},
	}
	_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected the protocol error, got %v", err)
	}
}

func TestSplitCommandHonoursQuotes(t *testing.T) {
	args, err := splitCommand(`hostname -f`)
	if err != nil || len(args) != 2 || args[1] != "-f" {
		t.Fatalf("args = %v err = %v", args, err)
	}
	args, err = splitCommand(`sh -c "printf '%s' hello world"`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(args) != 3 || args[2] != "printf '%s' hello world" {
		t.Fatalf("quoted argument = %#v", args)
	}
	if _, err := splitCommand(`echo "unbalanced`); err == nil {
		t.Fatal("an unbalanced quote must be an error")
	}
	if _, err := splitCommand("   "); err == nil {
		t.Fatal("an empty command must be an error")
	}
}

func TestScriptSourceReceivesTheQueryAsOneArgument(t *testing.T) {
	t.Setenv("LIFEOS_SCRIPT_HELPER", "1")
	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "Скрипт", Kind: domainresearch.KindScript, Mode: domainresearch.ModeRead,
		Endpoint: fmt.Sprintf("%q -test.run=TestScriptHelper {input}", os.Args[0]),
		Enabled:  true,
	}
	result, err := client.Call(context.Background(), connection, appresearch.CallRequest{Query: "два слова; rm -rf /"})
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if !strings.Contains(result.Body, "helper:два слова; rm -rf /") {
		t.Fatalf("the query must arrive as one argument, got %q", result.Body)
	}
}

// TestScriptHelper is the helper process the script tests run. It only acts
// when the parent marked it.
func TestScriptHelper(t *testing.T) {
	switch os.Getenv("LIFEOS_SCRIPT_HELPER") {
	case "1":
		fmt.Printf("helper:%s", os.Args[len(os.Args)-1])
		os.Exit(0)
	case "flood":
		// Write until the parent stops us: the point is that it does.
		chunk := []byte(strings.Repeat("шум ", 4096))
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(3)
			}
		}
	}
}

// A command that keeps writing must be stopped by the limit while it runs, not
// buffered until it finishes: a cap applied after the fact still lets a runaway
// process fill memory.
func TestScriptOutputIsBoundedWhileItIsProduced(t *testing.T) {
	t.Setenv("LIFEOS_SCRIPT_HELPER", "flood")
	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "Скрипт", Kind: domainresearch.KindScript, Mode: domainresearch.ModeRead,
		Endpoint: fmt.Sprintf("%q -test.run=TestScriptHelper", os.Args[0]),
		Enabled:  true,
	}
	result, err := client.Call(context.Background(), connection, appresearch.CallRequest{Query: "x"})
	if err == nil {
		t.Fatalf("a runaway command must be reported, got %d bytes", len(result.Body))
	}
	if !strings.Contains(err.Error(), "превысил допустимый размер") {
		t.Fatalf("the error must name the reason, got %v", err)
	}
	if len(result.Body) > scriptOutputLimit+64 {
		t.Fatalf("the body must stay within the limit, got %d bytes", len(result.Body))
	}
}

func TestScriptFailureIncludesStderr(t *testing.T) {
	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "Скрипт", Kind: domainresearch.KindScript, Mode: domainresearch.ModeRead,
		// A binary that does not exist is the cheapest deterministic failure on
		// every platform.
		Endpoint: "lifeos-nonexistent-command-xyz",
		Enabled:  true,
	}
	_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Query: "x"})
	if err == nil {
		t.Fatal("a missing command must fail")
	}
}

// decodeRequestBody reads a JSON body.
func decodeRequestBody(r *http.Request, target any) error {
	return json.NewDecoder(r.Body).Decode(target)
}

// ─── page reading stays on the public internet ──────────────────────────────

// A page address comes from a search result or from the model, so it must not
// reach the machine LifeOS runs on: a local admin panel, an open database port
// or a cloud metadata endpoint would all answer happily.
func TestFetchRefusesLocalAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><article><p>`+strings.Repeat("Внутренний сервис отвечает подробным текстом. ", 8)+`</p></article></body></html>`)
	}))
	defer server.Close()

	client := NewClient(Options{Timeout: 2 * time.Second})
	cases := []string{
		server.URL,
		strings.Replace(server.URL, "127.0.0.1", "localhost", 1),
		"http://127.0.0.1:8791/api/v1/tasks",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/admin",
		"http://192.168.1.1/",
		"http://[::1]:8080/",
	}
	for _, target := range cases {
		if _, err := client.Fetch(context.Background(), target); err == nil {
			t.Fatalf("page reading must refuse %s", target)
		} else if !strings.Contains(err.Error(), "внутреннюю сеть") && !strings.Contains(err.Error(), "не разрешён") {
			t.Fatalf("the refusal must say why, got %v for %s", err, target)
		}
	}
}

// A public page that redirects somewhere local is the same hole with a step in
// between: the redirect target is checked too.
func TestFetchRefusesRedirectToLocalAddress(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "внутренние данные")
	}))
	defer local.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, local.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// The redirector itself is allowed, as a search endpoint the user
	// configured would be; only the hop it points at is refused.
	client := NewClient(Options{Timeout: 2 * time.Second})
	_, err := client.Fetch(context.Background(), redirector.URL)
	if err == nil {
		t.Fatal("a redirect into the local network must be refused")
	}
	if !strings.Contains(err.Error(), "внутреннюю сеть") {
		t.Fatalf("the refusal must name the reason, got %v", err)
	}
}

// The address that is checked is the address that gets connected: a second DNS
// answer that points somewhere local must be checked again, or the name is
// simply a door into the internal network.
func TestGuardChecksEveryAnswerIncludingTheDial(t *testing.T) {
	guard := publicHostGuard{policy: policyPublicPage}
	if _, err := guard.resolveChecked(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("loopback must not resolve as a public host")
	}
	ips, err := guard.resolveChecked(context.Background(), "example.com")
	if err != nil || len(ips) == 0 {
		t.Fatalf("a public host resolves: %v %v", ips, err)
	}
	for _, ip := range ips {
		if !isPublicAddress(ip) {
			t.Fatalf("a public host resolved to a refused address: %v", ip)
		}
	}

	// DNS rebinding: the name answers as a public host once and as a local
	// one when the connection is made. The dial-time answer is checked too,
	// so the request fails instead of reaching the local machine.
	rebinding := &seqResolver{answers: map[string][][]net.IPAddr{
		"rebind.example.org": {
			{{IP: net.ParseIP("93.184.216.34")}},
			{{IP: net.ParseIP("127.0.0.1")}},
		},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the local answer must never be connected to")
	}))
	defer server.Close()
	guard = publicHostGuard{policy: policyPublicPage, resolver: rebinding}
	client := newGuardedHTTPClient(guard, 2*time.Second)
	_, err = client.Get("http://rebind.example.org/")
	if err == nil {
		t.Fatal("a name that rebinds to a local address must be refused")
	}
}

func TestPrivateAddressesAreRecognised(t *testing.T) {
	private := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.0.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0", "fd00::1", "::ffff:127.0.0.1",
	}
	for _, address := range private {
		if isPublicAddress(net.ParseIP(address)) {
			t.Fatalf("%s must not be treated as public", address)
		}
	}
	public := []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, address := range public {
		if !isPublicAddress(net.ParseIP(address)) {
			t.Fatalf("%s is a public address", address)
		}
	}
}

// Service addresses are refused in every policy, including the one that allows
// the user's own network: they answer about the machine LifeOS runs on. Some
// of them hide in ranges a private-source policy would otherwise accept.
func TestMetadataAddressesAreAlwaysRefused(t *testing.T) {
	addresses := []string{
		"169.254.169.254", // AWS, Google, Azure
		"169.254.170.2",   // AWS ECS task metadata
		"100.100.100.200", // Alibaba Cloud ECS (inside carrier-grade NAT)
		"192.0.0.192",     // Oracle Cloud
		"fd00:ec2::254",   // AWS (IPv6)
	}
	for _, address := range addresses {
		if !isMetadataAddress(net.ParseIP(address)) {
			t.Fatalf("%s is a service address", address)
		}
	}
	for _, address := range []string{"93.184.216.34", "10.1.2.3", "fd00::1"} {
		if isMetadataAddress(net.ParseIP(address)) {
			t.Fatalf("%s is not a service address", address)
		}
	}
	// The user's own network is allowed for their own source; a service
	// address is refused even there.
	guard := publicHostGuard{policy: policyUserSource, originHost: "100.100.100.200", allowLocal: true}
	if err := guard.checkURL(context.Background(), mustParseURL(t, "http://100.100.100.200/latest/meta-data/")); err == nil {
		t.Fatal("a service address is refused even for a local source")
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// seqResolver answers the same name with different addresses on successive
// lookups, which is what DNS rebinding looks like from the inside.
type seqResolver struct {
	mu      sync.Mutex
	answers map[string][][]net.IPAddr
	calls   map[string]int
}

func (r *seqResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	sequence := r.answers[host]
	if len(sequence) == 0 {
		return nil, fmt.Errorf("no such host: %s", host)
	}
	index := r.calls[host]
	r.calls[host] = index + 1
	if index >= len(sequence) {
		index = len(sequence) - 1
	}
	return sequence[index], nil
}

// staticResolver answers from a fixed table.
type staticResolver map[string][]net.IPAddr

func (r staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if addrs, ok := r[host]; ok {
		return addrs, nil
	}
	return nil, fmt.Errorf("no such host: %s", host)
}

// An MCP operation is a tool name, not an HTTP method: a source in read mode
// runs a tool only where the user approved it by name AND the server marks it
// read-only. Neither claim alone decides — the user's list is the permission,
// the annotation is the server describing itself.
func TestMCPReadModeRefusesChangingTools(t *testing.T) {
	var called []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = decodeRequestBody(r, &message)
		called = append(called, message.Method+" "+message.Params.Name)
		switch message.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-1")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[
				{"name":"delete_repo","annotations":{"readOnlyHint":false}},
				{"name":"unannotated_tool"},
				{"name":"read_repo","annotations":{"readOnlyHint":true}}
			]}}`)
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	client := newTestClient(Options{})
	read := &domainresearch.Connection{
		ID: 1, Name: "MCP", Kind: domainresearch.KindMCP, Mode: domainresearch.ModeRead,
		Endpoint: server.URL + "/mcp", Enabled: true,
		// The user approved these tools for reading: the approval is per tool
		// and it is theirs, not the server's.
		AllowedTools: []string{"read_repo", "unannotated_tool", "unknown_tool", "delete_repo"},
	}
	// Approved or not, a tool the server does not call read-only is a changing
	// call: the annotation is the only description of the effect there is, and
	// it is not a guarantee.
	for _, tool := range []string{"delete_repo", "unannotated_tool"} {
		_, err := client.Call(context.Background(), read, appresearch.CallRequest{Operation: tool})
		if !errors.Is(err, appresearch.ErrSourceReadOnly) {
			t.Fatalf("%s must be refused in read mode, got %v", tool, err)
		}
	}
	// A tool outside the user's list is refused before the server is even
	// asked: the list is the permission.
	notApproved := *read
	notApproved.AllowedTools = []string{"read_repo"}
	_, err := client.Call(context.Background(), &notApproved, appresearch.CallRequest{Operation: "delete_repo"})
	if !errors.Is(err, appresearch.ErrSourceReadOnly) {
		t.Fatalf("a tool outside the user's list must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "не входит в список разрешённых") {
		t.Fatalf("the refusal must name the reason, got %v", err)
	}
	// A tool the server does not list at all is a bad request, not a
	// permission problem — and it is refused just the same.
	if _, err := client.Call(context.Background(), read, appresearch.CallRequest{Operation: "unknown_tool"}); !errors.Is(err, appresearch.ErrInvalidCall) {
		t.Fatalf("an unknown tool must be refused, got %v", err)
	}
	for _, entry := range called {
		if strings.HasPrefix(entry, "tools/call") {
			t.Fatalf("a refused tool must never be called: %v", called)
		}
	}
	// The tool the user approved and the server marks read-only works without
	// a write intent.
	if _, err := client.Call(context.Background(), read, appresearch.CallRequest{Operation: "read_repo"}); err != nil {
		t.Fatalf("an approved read-only tool must work in read mode: %v", err)
	}
	// A stated write intent reaches a changing tool on a writable source.
	write := *read
	write.Mode = domainresearch.ModeWrite
	if _, err := client.Call(context.Background(), &write, appresearch.CallRequest{Operation: "delete_repo", Write: true}); err != nil {
		t.Fatalf("a changing tool with a write intent must run: %v", err)
	}
	if !strings.Contains(strings.Join(called, ","), "tools/call delete_repo") {
		t.Fatalf("the changing tool was never called: %v", called)
	}
}

// The server's annotation is a claim by the server: one that marks its own
// delete-everything tool "read-only" is exactly the server LifeOS must not
// believe. The user's per-tool approval is what lets a tool run without a
// write, and without it the call never leaves — even for a tool the server
// swears is harmless.
func TestMCPReadModeDoesNotTrustTheServersAnnotation(t *testing.T) {
	var called []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = decodeRequestBody(r, &message)
		called = append(called, message.Method+" "+message.Params.Name)
		switch message.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-1")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[
				{"name":"delete_all_data","annotations":{"readOnlyHint":true}}
			]}}`)
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "MCP", Kind: domainresearch.KindMCP, Mode: domainresearch.ModeRead,
		Endpoint: server.URL + "/mcp", Enabled: true,
	}
	_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "delete_all_data"})
	if !errors.Is(err, appresearch.ErrSourceReadOnly) {
		t.Fatalf("a lying annotation must not grant read access, got %v", err)
	}
	for _, entry := range called {
		if strings.HasPrefix(entry, "tools/call") {
			t.Fatalf("the tool must never be called: %v", called)
		}
	}
	// Once the user approves the tool by name, their decision stands: the
	// call runs, and the responsibility for it is the user's.
	connection.AllowedTools = []string{"delete_all_data"}
	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "delete_all_data"}); err != nil {
		t.Fatalf("a tool the user approved must run: %v", err)
	}
}

// A connected source may point at the user's own network, but the service
// metadata range is never a service someone connects on purpose: it is the
// classic escalation target from an address a source is allowed to use.
func TestSourceEndpointRefusesMetadataRange(t *testing.T) {
	client := newTestClient(Options{})
	connection := &domainresearch.Connection{
		ID: 1, Name: "Метаданные", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "http://169.254.169.254/latest/meta-data/", Enabled: true,
	}
	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{Method: "GET"}); err == nil {
		t.Fatal("the metadata address must be refused")
	}
	connection.Endpoint = "http://[fd00:ec2::254]/latest/meta-data/"
	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{Method: "GET"}); err == nil {
		t.Fatal("the IPv6 metadata address must be refused")
	}
	for _, endpoint := range []string{"http://100.100.100.200/latest/meta-data/", "http://192.0.0.192/opc/v2/instance/"} {
		connection.Endpoint = endpoint
		_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Method: "GET"})
		if err == nil {
			t.Fatalf("%s must be refused", endpoint)
		}
		// The refusal must be the guard's, not a network timeout that happens
		// to end the request the same way.
		if !strings.Contains(err.Error(), "служебного сервиса") {
			t.Fatalf("%s: expected the service-address refusal, got %v", endpoint, err)
		}
	}
	// A search source may not point there either: it is a source like any
	// other, and its results are pages LifeOS then reads.
	search := &domainresearch.Connection{
		ID: 2, Name: "Поиск в метаданных", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Endpoint: "http://100.100.100.200/latest/meta-data/?q={query}", Format: domainresearch.FormatSearxng,
		Enabled: true,
	}
	if _, err := client.Search(context.Background(), search, "q", 5); err == nil {
		t.Fatal("a search source on a service address must be refused")
	}
	// A local service is a legitimate source: the address was typed by the user.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	connection.Endpoint = server.URL
	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{Method: "GET"}); err != nil {
		t.Fatalf("a local source must still work: %v", err)
	}
}

// A source may live on the user's own network, but only the source: a redirect
// to another machine of that network is not the address the user typed, and
// following it would make the source a door into the internal network.
func TestSourceRedirectToLocalMachineIsRefused(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the internal machine must never be reached")
	}))
	defer inner.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://inner.local.test/admin", http.StatusFound)
	}))
	defer front.Close()
	frontPort := strings.TrimPrefix(front.URL, "http://127.0.0.1:")

	resolver := staticResolver{
		"api.local.test":   {{IP: net.ParseIP("127.0.0.1")}},
		"inner.local.test": {{IP: net.ParseIP("10.0.0.5")}},
	}
	// The guard is strict: only the source host itself may be local.
	client := NewClient(Options{Timeout: 2 * time.Second, Resolver: resolver})

	connection := &domainresearch.Connection{
		ID: 1, Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "http://api.local.test:" + frontPort + "/v1", Enabled: true,
	}
	_, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "/status", Method: "GET"})
	if err == nil {
		t.Fatal("a redirect into the internal network must be refused")
	}
	if !strings.Contains(err.Error(), "внутреннюю сеть") {
		t.Fatalf("the refusal must name the reason, got %v", err)
	}

	// The same door through an MCP server: its answers come over HTTP too.
	mcp := *connection
	mcp.Kind = domainresearch.KindMCP
	mcp.Endpoint = "http://api.local.test:" + frontPort + "/mcp"
	if _, err := client.Call(context.Background(), &mcp, appresearch.CallRequest{Operation: "tools/list"}); err == nil {
		t.Fatal("an MCP redirect into the internal network must be refused")
	}

	// The source host itself is the user's choice and stays reachable on the
	// local network: the refusal is about the redirect, not about locality.
	resolver["api.local.test"] = []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}
	resolver["inner.local.test"] = []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}
	same := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer same.Close()
	samePort := strings.TrimPrefix(same.URL, "http://127.0.0.1:")
	connection.Endpoint = "http://api.local.test:" + samePort
	if _, err := client.Call(context.Background(), connection, appresearch.CallRequest{Operation: "/status"}); err != nil {
		t.Fatalf("a local source the user named must work: %v", err)
	}
}

// A name that is public once and local at connection time is the same door:
// every answer is checked, including the one used to connect.
func TestDNSRebindingIsRefusedAtConnectionTime(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the rebound answer must never be connected to")
	}))
	defer local.Close()

	rebinding := &seqResolver{answers: map[string][][]net.IPAddr{
		"page.example.org": {
			{{IP: net.ParseIP("93.184.216.34")}},
			{{IP: net.ParseIP("127.0.0.1")}},
		},
	}}
	client := NewClient(Options{Timeout: 2 * time.Second, Resolver: rebinding})
	if _, err := client.Fetch(context.Background(), "http://page.example.org/doc"); err == nil {
		t.Fatal("a page whose name rebounds to a local address must be refused")
	}
}
