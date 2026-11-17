package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// httpTestWeb is the deterministic outside world for the HTTP tests: two sites
// that agree about one release and one page that never opens.
type httpTestWeb struct {
	mu      sync.Mutex
	queries []string
	calls   []appresearch.CallRequest
}

func newHTTPTestWeb() *httpTestWeb { return &httpTestWeb{} }

func (w *httpTestWeb) Search(_ context.Context, _ *domainresearch.Connection, query string, limit int) ([]appresearch.SearchResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.queries = append(w.queries, query)
	results := []appresearch.SearchResult{
		{Title: "Go 1.24 release notes", URL: "https://go.dev/doc/go1.24", Snippet: "Generic type aliases."},
		{Title: "Go 1.24 review", URL: "https://example.org/go124", Snippet: "Generic type aliases are here."},
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (w *httpTestWeb) Fetch(_ context.Context, rawURL string) (appresearch.Page, error) {
	switch rawURL {
	case "https://go.dev/doc/go1.24":
		return appresearch.Page{
			URL:   rawURL,
			Title: "Go 1.24 release notes",
			Text: "The release adds generic type aliases to the Go language, so a package can name a parameterised type. " +
				"They are available in Go 1.24 and in later toolchain versions.",
		}, nil
	case "https://example.org/go124":
		return appresearch.Page{
			URL:   rawURL,
			Title: "Go 1.24 review",
			Text: "In Go 1.24 the Go language adds generic type aliases, letting a package name a parameterised type without a wrapper. " +
				"The feature is stable in the release.",
		}, nil
	}
	return appresearch.Page{}, fmt.Errorf("страница %s недоступна", rawURL)
}

func (w *httpTestWeb) Call(_ context.Context, _ *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, request)
	return appresearch.CallResult{Status: 200, ContentType: "application/json", Body: `{"items":[]}`}, nil
}

func (w *httpTestWeb) recordedCalls() []appresearch.CallRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]appresearch.CallRequest(nil), w.calls...)
}

func researchRequest(t *testing.T, handler *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	return recorder
}

func TestResearchRunOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	w := researchRequest(t, handler, http.MethodPost, "/api/v1/research/reports", `{
		"question": "Что нового для generic type aliases в Go 1.24?",
		"queries": ["go 1.24 generic type aliases"],
		"save_knowledge": false
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("run research: status %d body %s", w.Code, w.Body.String())
	}
	var report researchReportBody
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Status != "completed" {
		t.Fatalf("status = %q", report.Status)
	}
	if len(report.Findings) == 0 {
		t.Fatalf("no findings: %+v", report)
	}
	if report.Findings[0].Certainty != "fact" {
		t.Fatalf("two independent sites must be a fact, got %q", report.Findings[0].Certainty)
	}
	if len(report.Findings[0].Citations) != 2 || report.Findings[0].Citations[0].Site == "" {
		t.Fatalf("citations = %+v", report.Findings[0].Citations)
	}
	if !strings.Contains(report.Summary, "Подтверждено несколькими источниками") {
		t.Fatalf("summary = %q", report.Summary)
	}

	// The list and the detail endpoint return the same record.
	w = researchRequest(t, handler, http.MethodGet, "/api/v1/research/reports", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", w.Code, w.Body.String())
	}
	var list struct {
		Data []researchReportBody `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 1 || len(list.Data) != 1 || list.Data[0].ID != report.ID {
		t.Fatalf("list = %+v", list)
	}

	w = researchRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/research/reports/%d", report.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: status %d body %s", w.Code, w.Body.String())
	}
	w = researchRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/research/reports/%d", report.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status %d body %s", w.Code, w.Body.String())
	}
	w = researchRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/research/reports/%d", report.ID), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted report: status %d body %s", w.Code, w.Body.String())
	}
}

func TestResearchRunRequiresAQuestion(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()
	w := researchRequest(t, handler, http.MethodPost, "/api/v1/research/reports", `{"question": ""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestResearchSourceLifecycleOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	w := researchRequest(t, handler, http.MethodPost, "/api/v1/research/sources", `{
		"name": "API",
		"kind": "http_api",
		"mode": "read",
		"endpoint": "https://api.example.com/v1",
		"enabled": true
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create source: status %d body %s", w.Code, w.Body.String())
	}
	var source researchSourceBody
	if err := json.Unmarshal(w.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if source.Kind != "http_api" || !source.Enabled || source.Mode != "read" {
		t.Fatalf("source = %+v", source)
	}

	// The user may connect a script source: it is their own instrument. It is
	// read-only by construction, so asking for writes is refused at creation.
	w = researchRequest(t, handler, http.MethodPost, "/api/v1/research/sources", `{
		"name": "Скрипт",
		"kind": "script",
		"mode": "write",
		"endpoint": "hostname"
	}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("writable script: status %d body %s", w.Code, w.Body.String())
	}
	w = researchRequest(t, handler, http.MethodPost, "/api/v1/research/sources", `{
		"name": "Скрипт",
		"kind": "script",
		"mode": "read",
		"endpoint": "hostname"
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create script: status %d body %s", w.Code, w.Body.String())
	}

	// A read-only source refuses an outbound change, and the refusal is
	// journalled for the audit.
	w = researchRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/research/sources/%d/call", source.ID), `{
		"operation": "/items",
		"method": "POST",
		"body": "{}"
	}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("write on read-only source: status %d body %s", w.Code, w.Body.String())
	}
	w = researchRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/research/sources/%d/calls", source.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("calls: status %d body %s", w.Code, w.Body.String())
	}
	var calls struct {
		Data []researchCallBody `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &calls); err != nil {
		t.Fatalf("decode calls: %v", err)
	}
	if len(calls.Data) != 1 || calls.Data[0].Outcome != "refused" {
		t.Fatalf("calls = %+v", calls.Data)
	}

	// Granting writes is a user decision and takes the version.
	w = researchRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/research/sources/%d", source.ID),
		fmt.Sprintf(`{"mode": "write", "expected_version": %d}`, source.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("grant writes: status %d body %s", w.Code, w.Body.String())
	}
	var writable researchSourceBody
	if err := json.Unmarshal(w.Body.Bytes(), &writable); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if writable.Mode != "write" {
		t.Fatalf("mode = %q", writable.Mode)
	}

	// A stale version is a conflict, not a silent overwrite.
	w = researchRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/research/sources/%d", source.ID),
		fmt.Sprintf(`{"mode": "read", "expected_version": %d}`, source.Version))
	if w.Code != http.StatusConflict {
		t.Fatalf("stale patch: status %d body %s", w.Code, w.Body.String())
	}

	w = researchRequest(t, handler, http.MethodGet, "/api/v1/research/sources", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list sources: status %d body %s", w.Code, w.Body.String())
	}

	w = researchRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/research/sources/%d", source.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete source: status %d body %s", w.Code, w.Body.String())
	}
	w = researchRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/research/sources/%d", source.ID), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete again: status %d body %s", w.Code, w.Body.String())
	}
}

func TestResearchWatchLifecycleOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	w := researchRequest(t, handler, http.MethodPost, "/api/v1/research/watches", `{
		"topic": "Релизы Go",
		"query": "go release notes",
		"cadence": "daily"
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create watch: status %d body %s", w.Code, w.Body.String())
	}
	var watch researchWatchBody
	if err := json.Unmarshal(w.Body.Bytes(), &watch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if watch.Cadence != "daily" || watch.NextCheckAt == nil {
		t.Fatalf("watch = %+v", watch)
	}

	w = researchRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/research/watches/%d/check", watch.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("check watch: status %d body %s", w.Code, w.Body.String())
	}
	var checked struct {
		Watch  researchWatchBody `json:"watch"`
		Change struct {
			Summary   string `json:"summary"`
			New       []any  `json:"new"`
			Unchanged int    `json:"unchanged"`
		} `json:"change"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checked); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if len(checked.Change.New) != 2 {
		t.Fatalf("the first check sees both pages as new, got %+v", checked.Change)
	}
	if checked.Watch.LastCheckedAt == nil || checked.Watch.LastChangeSummary == "" {
		t.Fatalf("watch after check = %+v", checked.Watch)
	}

	w = researchRequest(t, handler, http.MethodGet, "/api/v1/research/watches", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list watches: status %d body %s", w.Code, w.Body.String())
	}
	var watches struct {
		Data []researchWatchBody `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &watches); err != nil {
		t.Fatalf("decode watches: %v", err)
	}
	if len(watches.Data) != 1 {
		t.Fatalf("watches = %+v", watches.Data)
	}

	// Pausing a watch keeps it out of the automatic checks but keeps the row.
	w = researchRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/research/watches/%d", watch.ID),
		fmt.Sprintf(`{"enabled": false, "expected_version": %d}`, checked.Watch.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("pause watch: status %d body %s", w.Code, w.Body.String())
	}
	var paused researchWatchBody
	if err := json.Unmarshal(w.Body.Bytes(), &paused); err != nil {
		t.Fatalf("decode paused: %v", err)
	}
	if paused.Enabled || paused.NextCheckAt != nil {
		t.Fatalf("paused = %+v", paused)
	}

	w = researchRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/research/watches/%d", watch.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete watch: status %d body %s", w.Code, w.Body.String())
	}
}
