package research

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// blockTags end a line when readable text is extracted, so the sentence
// splitter in the application layer sees paragraphs rather than one long
// ribbon of words.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true, "main": true,
	"nav": true, "aside": true, "blockquote": true, "table": true, "pre": true,
	"dt": true, "dd": true, "ul": true, "ol": true, "figure": true, "figcaption": true,
	"form": true, "hr": true, "title": true,
}

// skipTags carry no readable content, and their text is usually code, styles
// or navigation chrome that would pollute the passages.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true, "template": true,
	"iframe": true, "canvas": true, "head": true, "nav": true, "footer": true,
	"aside": true, "form": true, "button": true, "select": true, "option": true,
	"meta": true, "link": true,
}

// readableContent turns a response body into a title and readable text. HTML
// is walked and reduced; plain text and JSON are passed through, because a
// connected API answering JSON is still a source LifeOS can read.
func readableContent(body []byte, contentType string) (title, text string) {
	kind := strings.ToLower(contentType)
	switch {
	case strings.Contains(kind, "json"):
		return "", string(body)
	case strings.Contains(kind, "text/plain"), strings.Contains(kind, "text/markdown"),
		strings.Contains(kind, "text/csv"), strings.Contains(kind, "xml"):
		return "", string(body)
	}
	decoded, err := decodeCharset(body, contentType)
	if err != nil {
		decoded = body
	}
	document, err := html.Parse(bytes.NewReader(decoded))
	if err != nil {
		return "", strings.Join(strings.Fields(string(decoded)), " ")
	}
	var titleBuilder strings.Builder
	var textBuilder strings.Builder
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, skipping bool) {
		if node.Type == html.ElementNode {
			if skipTags[strings.ToLower(node.Data)] {
				skipping = true
			}
			if strings.EqualFold(node.Data, "title") && node.FirstChild != nil && titleBuilder.Len() == 0 {
				titleBuilder.WriteString(node.FirstChild.Data)
			}
		}
		if !skipping && node.Type == html.TextNode {
			textBuilder.WriteString(node.Data)
			textBuilder.WriteString(" ")
		}
		if !skipping && node.Type == html.ElementNode && blockTags[strings.ToLower(node.Data)] {
			textBuilder.WriteString("\n")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, skipping)
		}
		if !skipping && node.Type == html.ElementNode && blockTags[strings.ToLower(node.Data)] {
			textBuilder.WriteString("\n")
		}
	}
	visit(document, false)
	return cleanText(titleBuilder.String()), readableText(textBuilder.String())
}

// readableText drops empty lines, collapses runs of spaces and keeps
// paragraph breaks.
func readableText(raw string) string {
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.Join(strings.Fields(line), " ")
		if trimmed == "" {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}

// decodeCharset converts the body to UTF-8 using the response content type or,
// when the server is silent, the document's own <meta charset>. Russian pages
// still come in windows-1251 often enough that guessing UTF-8 is not enough.
func decodeCharset(body []byte, contentType string) ([]byte, error) {
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}
