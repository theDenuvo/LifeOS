package research

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
)

// parseSearxng reads the JSON answer of a SearxNG instance.
func parseSearxng(body []byte, limit int) ([]appresearch.SearchResult, error) {
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("ответ поиска не разобран как JSON: %w", err)
	}
	results := make([]appresearch.SearchResult, 0, len(payload.Results))
	for _, item := range payload.Results {
		link := strings.TrimSpace(item.URL)
		if link == "" {
			continue
		}
		snippet := item.Content
		if strings.TrimSpace(snippet) == "" {
			snippet = item.Snippet
		}
		results = append(results, appresearch.SearchResult{
			Title:   cleanText(item.Title),
			URL:     link,
			Snippet: cleanText(snippet),
		})
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

// parseDuckDuckGo reads the HTML answer of the DuckDuckGo endpoint. The markup
// is not a documented API, so the parser keys off the class names the endpoint
// has used for years and tolerates their absence instead of failing.
func parseDuckDuckGo(body []byte, contentType string, limit int) ([]appresearch.SearchResult, error) {
	if strings.Contains(strings.ToLower(contentType), "json") {
		// Someone pointed the search URL at a JSON API: read it as SearxNG,
		// which is the JSON shape LifeOS understands.
		return parseSearxng(body, limit)
	}
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ответ поиска не разобран: %w", err)
	}
	links := make([]appresearch.SearchResult, 0, 16)
	snippets := make([]string, 0, 16)
	walk(document, func(node *html.Node) {
		if node.Type != html.ElementNode || node.Data != "a" && node.Data != "div" && node.Data != "span" && node.Data != "td" {
			return
		}
		class := attribute(node, "class")
		switch {
		case node.Data == "a" && (hasClass(class, "result__a") || hasClass(class, "result-link")):
			href := decodeDuckDuckGoLink(attribute(node, "href"))
			if href == "" {
				return
			}
			links = append(links, appresearch.SearchResult{
				Title: cleanText(textOf(node)),
				URL:   href,
			})
		case hasClass(class, "result__snippet") || hasClass(class, "result-snippet"):
			snippets = append(snippets, cleanText(textOf(node)))
		}
	})
	result := make([]appresearch.SearchResult, 0, len(links))
	for i, link := range links {
		if i < len(snippets) {
			link.Snippet = snippets[i]
		}
		result = append(result, link)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

// decodeDuckDuckGoLink unwraps the redirect the endpoint wraps results in.
func decodeDuckDuckGoLink(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if target := parsed.Query().Get("uddg"); target != "" {
		if decoded, err := url.QueryUnescape(target); err == nil {
			return decoded
		}
		return target
	}
	if strings.HasPrefix(href, "/") {
		// A relative link with no target is not a result.
		return ""
	}
	return href
}

// walk visits every node depth-first.
func walk(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}

// attribute returns an element attribute, or "".
func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
}

// hasClass reports whether the class attribute contains one class.
func hasClass(class, want string) bool {
	for _, field := range strings.Fields(class) {
		if field == want {
			return true
		}
	}
	return false
}

// textOf returns the text inside a node.
func textOf(node *html.Node) string {
	var buffer strings.Builder
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.TextNode {
			buffer.WriteString(current.Data)
		}
		if current.Type == html.ElementNode && (current.Data == "script" || current.Data == "style") {
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return buffer.String()
}

// cleanText collapses whitespace and decodes the entities search results use.
func cleanText(value string) string {
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}
