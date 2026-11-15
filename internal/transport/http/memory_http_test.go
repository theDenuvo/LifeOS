package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func memoryRequest(t *testing.T, handler *Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	return recorder
}

func createMemoryEntry(t *testing.T, handler *Handler, payload string) memoryBody {
	t.Helper()
	w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory", payload, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create memory: status %d body %s", w.Code, w.Body.String())
	}
	var response struct {
		Entry memoryBody `json:"entry"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return response.Entry
}

func TestMemoryCRUDOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	created := createMemoryEntry(t, handler, `{"title":"Формат отчётов","content":"Короткие отчёты с таблицами","category":"preference"}`)
	if created.ID == 0 || created.Version != 1 || created.Area != "personal" {
		t.Fatalf("created entry = %+v", created)
	}
	if len(created.Sources) != 1 || created.Sources[0].Kind != "user" {
		t.Fatalf("provenance = %+v", created.Sources)
	}

	w := memoryRequest(t, handler, http.MethodGet, "/api/v1/memory?area=personal", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var list struct {
		Data []memoryBody `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 1 || len(list.Data) != 1 {
		t.Fatalf("list total = %d, items = %d", list.Meta.Total, len(list.Data))
	}

	// A mutation without If-Match must be refused, not silently applied.
	if w := memoryRequest(t, handler, http.MethodPatch, "/api/v1/memory/"+strconv.FormatInt(created.ID, 10), `{"content":"Другое"}`, nil); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconditional patch status = %d, want 428", w.Code)
	}

	w = memoryRequest(t, handler, http.MethodPatch, "/api/v1/memory/"+strconv.FormatInt(created.ID, 10), `{"content":"Только таблицы"}`, map[string]string{"If-Match": `"1"`})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", w.Code, w.Body.String())
	}
	var updated memoryBody
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if updated.Content != "Только таблицы" || updated.Version != 2 {
		t.Fatalf("updated = %+v", updated)
	}

	// A stale version is a conflict.
	if w := memoryRequest(t, handler, http.MethodPatch, "/api/v1/memory/"+strconv.FormatInt(created.ID, 10), `{"content":"Ещё"}`, map[string]string{"If-Match": `"1"`}); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale patch status = %d, want 412", w.Code)
	}

	if w := memoryRequest(t, handler, http.MethodDelete, "/api/v1/memory/"+strconv.FormatInt(created.ID, 10), "", map[string]string{"If-Match": `"2"`}); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}
	if w := memoryRequest(t, handler, http.MethodGet, "/api/v1/memory/"+strconv.FormatInt(created.ID, 10), "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d, want 404", w.Code)
	}
}

func TestMemorySearchAndValidationOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	createMemoryEntry(t, handler, `{"title":"Проект Atlas","content":"Портал Atlas пишется на Go","category":"context"}`)
	createMemoryEntry(t, handler, `{"title":"Редактор","content":"Использую Neovim","category":"preference"}`)

	w := memoryRequest(t, handler, http.MethodGet, "/api/v1/memory?q=Atlas", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("search: status %d", w.Code)
	}
	var list struct {
		Data []memoryBody `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].Title != "Проект Atlas" {
		t.Fatalf("search results = %+v", list.Data)
	}

	if w := memoryRequest(t, handler, http.MethodGet, "/api/v1/memory?area=unknown", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid area status = %d, want 400", w.Code)
	}
	if w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory", `{"title":"","content":"x","category":"preference"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty title status = %d, want 400", w.Code)
	}
	if w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory", `{"title":"Тема","content":"x","category":"nonsense"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid category status = %d, want 400", w.Code)
	}
}

func TestMemoryConflictIsVisibleAndResolvableOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()
	ctx := t.Context()
	_ = ctx

	first := createMemoryEntry(t, handler, `{"title":"Часовой пояс","content":"Живу в Москве","category":"fact","certainty":"fact"}`)

	// The same subject with a different claim becomes a reviewable conflict.
	w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory", `{"title":"Часовой пояс","content":"Живу в Екатеринбурге","category":"fact","certainty":"assumption"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("second create: status %d body %s", w.Code, w.Body.String())
	}
	var conflictResponse struct {
		ConflictID int64 `json:"conflict_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflictResponse); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflictResponse.ConflictID == 0 {
		t.Fatalf("conflict was not created: %s", w.Body.String())
	}

	w = memoryRequest(t, handler, http.MethodGet, "/api/v1/memory/conflicts", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("conflicts: status %d", w.Code)
	}
	var conflicts struct {
		Data []memoryConflictBody `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflicts); err != nil {
		t.Fatalf("decode conflicts: %v", err)
	}
	if len(conflicts.Data) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts.Data))
	}
	if conflicts.Data[0].Existing == nil || conflicts.Data[0].Candidate == nil {
		t.Fatal("both sides of the conflict must be returned")
	}

	w = memoryRequest(t, handler, http.MethodPost, "/api/v1/memory/conflicts/"+strconv.FormatInt(conflictResponse.ConflictID, 10)+"/resolve", `{"resolution":"candidate"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: status %d body %s", w.Code, w.Body.String())
	}
	w = memoryRequest(t, handler, http.MethodGet, "/api/v1/memory/"+strconv.FormatInt(first.ID, 10), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get superseded: status %d", w.Code)
	}
	var superseded memoryBody
	if err := json.Unmarshal(w.Body.Bytes(), &superseded); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if superseded.Status != "superseded" || superseded.SupersededBy == nil {
		t.Fatalf("superseded entry = %+v", superseded)
	}

	if w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory/conflicts/"+strconv.FormatInt(conflictResponse.ConflictID, 10)+"/resolve", `{"resolution":"both"}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("re-resolve status = %d, want 409", w.Code)
	}
}

func TestMemoryStatsAndLinksOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	entry := createMemoryEntry(t, handler, `{"title":"Atlas","content":"Портал Atlas","category":"context"}`)
	w := memoryRequest(t, handler, http.MethodPost, "/api/v1/memory/"+strconv.FormatInt(entry.ID, 10)+"/links", `{"target_type":"task","target_id":5,"relation":"относится к"}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("link to missing task status = %d, want 422", w.Code)
	}

	// Create a real task, then link the entry to it.
	taskResponse := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"Написать отчёт"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.Routes().ServeHTTP(taskResponse, request)
	if taskResponse.Code != http.StatusCreated {
		t.Fatalf("create task: status %d body %s", taskResponse.Code, taskResponse.Body.String())
	}
	var task taskBody
	if err := json.Unmarshal(taskResponse.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	w = memoryRequest(t, handler, http.MethodPost, "/api/v1/memory/"+strconv.FormatInt(entry.ID, 10)+"/links", `{"target_type":"task","target_id":`+strconv.FormatInt(task.ID, 10)+`,"relation":"относится к"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("link: status %d body %s", w.Code, w.Body.String())
	}
	var link memoryLinkBody
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if link.ID == 0 || link.TargetID != task.ID {
		t.Fatalf("link = %+v", link)
	}

	w = memoryRequest(t, handler, http.MethodGet, "/api/v1/memory/"+strconv.FormatInt(entry.ID, 10), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get entry: status %d", w.Code)
	}
	var detailed memoryBody
	if err := json.Unmarshal(w.Body.Bytes(), &detailed); err != nil {
		t.Fatalf("decode detailed: %v", err)
	}
	if len(detailed.Links) != 1 {
		t.Fatalf("links = %d, want 1", len(detailed.Links))
	}

	w = memoryRequest(t, handler, http.MethodGet, "/api/v1/memory/stats", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status %d", w.Code)
	}
	var stats struct {
		Personal         int `json:"personal"`
		Knowledge        int `json:"knowledge"`
		PendingConflicts int `json:"pending_conflicts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Personal != 1 || stats.Knowledge != 0 || stats.PendingConflicts != 0 {
		t.Fatalf("stats = %+v", stats)
	}

	if w := memoryRequest(t, handler, http.MethodDelete, "/api/v1/memory/"+strconv.FormatInt(entry.ID, 10)+"/links/"+strconv.FormatInt(link.ID, 10), "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("unlink status = %d, want 204", w.Code)
	}
}
