package httptransport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

type apiClock struct {
	now time.Time
}

func (c apiClock) Now() time.Time { return c.now }

func newTaskAPI(t *testing.T, now time.Time) (*Handler, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := sqlite.Open(tmpDir + "/api.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := sqlite.Migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{
		DataDir:  tmpDir,
		Timezone: "Europe/Moscow",
	}
	clock := apiClock{now: now}
	service := tasks.NewService(sqlite.NewTaskUnitOfWork(db), clock, tasks.WithDefaultTimezone("Europe/Moscow"))
	inboxService := inbox.NewService(sqlite.NewInboxUnitOfWork(db), clock, "Europe/Moscow")
	summaryService := summary.NewService(service, inboxService, clock, "Europe/Moscow")
	handler := NewHandler(db, cfg, testEmbedFS, Services{
		Tasks:   service,
		Inbox:   inboxService,
		Summary: summaryService,
	})
	handler.now = func() time.Time { return now }

	return handler, func() { _ = db.Close() }
}

func apiRequest(t *testing.T, handler *Handler, method, path string, body any, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else if raw, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(raw))
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, req)

	var decoded map[string]any
	if recorder.Body.Len() > 0 {
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	}
	return recorder, decoded
}

func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, want, recorder.Body.String())
	}
}

func TestTaskAPIEndToEnd(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, created := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{
		"title":        "Подготовить презентацию",
		"note":         "Слайды 1-15",
		"priority":     "high",
		"planned_date": "2026-11-12",
		"due_date":     "2026-11-13",
		"due_at":       "2026-11-13T18:30:00Z",
		"tags":         []string{"работа", "совет"},
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	if got := recorder.Header().Get("ETag"); got != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", got)
	}
	if created["version"] != float64(1) {
		t.Errorf("version = %v, want 1", created["version"])
	}
	if created["status"] != "open" {
		t.Errorf("status = %v, want open", created["status"])
	}
	if created["timezone"] != "Europe/Moscow" {
		t.Errorf("timezone = %v, want Europe/Moscow", created["timezone"])
	}
	if created["planned_date"] != "2026-11-12" || created["due_date"] != "2026-11-13" {
		t.Errorf("dates = %v / %v", created["planned_date"], created["due_date"])
	}
	if created["overdue"] != false {
		t.Errorf("overdue = %v, want false", created["overdue"])
	}
	id := int64(created["id"].(float64))

	recorder, fetched := apiRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", id), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if fetched["title"] != "Подготовить презентацию" {
		t.Errorf("title = %v", fetched["title"])
	}
	if recorder.Header().Get("ETag") != `"1"` {
		t.Errorf("GET ETag = %q", recorder.Header().Get("ETag"))
	}

	// Mutations require If-Match: optimistic locking must not degrade to
	// last-write-wins when a client forgets the token.
	recorder, problem := apiRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/tasks/%d", id),
		map[string]any{"priority": "low"}, nil)
	requireStatus(t, recorder, http.StatusPreconditionRequired)
	if problem["status"] != float64(428) {
		t.Errorf("problem status = %v", problem["status"])
	}

	recorder, patched := apiRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/tasks/%d", id),
		map[string]any{"priority": "low", "note": nil}, map[string]string{"If-Match": `"1"`})
	requireStatus(t, recorder, http.StatusOK)
	if patched["priority"] != "low" {
		t.Errorf("priority = %v, want low", patched["priority"])
	}
	if patched["note"] != nil {
		t.Errorf("note = %v, want null", patched["note"])
	}
	if patched["version"] != float64(2) {
		t.Errorf("version = %v, want 2", patched["version"])
	}

	recorder, _ = apiRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/tasks/%d", id),
		map[string]any{"title": "stale"}, map[string]string{"If-Match": `"1"`})
	requireStatus(t, recorder, http.StatusConflict)

	recorder, completed := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", id), nil,
		map[string]string{"If-Match": `"2"`})
	requireStatus(t, recorder, http.StatusOK)
	if completed["status"] != "completed" || completed["completed_at"] == nil {
		t.Errorf("completed = %v / %v", completed["status"], completed["completed_at"])
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", id), nil,
		map[string]string{"If-Match": `"3"`})
	requireStatus(t, recorder, http.StatusPreconditionFailed)

	recorder, restored := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/restore", id), nil,
		map[string]string{"If-Match": `"3"`})
	requireStatus(t, recorder, http.StatusOK)
	if restored["status"] != "open" || restored["completed_at"] != nil {
		t.Errorf("restored = %v / %v", restored["status"], restored["completed_at"])
	}

	recorder, rescheduled := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/reschedule", id),
		map[string]any{"due_date": "2026-11-20", "due_at": nil},
		map[string]string{"If-Match": `"4"`})
	requireStatus(t, recorder, http.StatusOK)
	if rescheduled["due_date"] != "2026-11-20" || rescheduled["due_at"] != nil {
		t.Errorf("rescheduled = %v / %v", rescheduled["due_date"], rescheduled["due_at"])
	}

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks?priority=low&status=open", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	meta := list["meta"].(map[string]any)
	if meta["total"] != float64(1) {
		t.Errorf("total = %v, want 1", meta["total"])
	}
}

func TestTaskAPIFiltersAndOverdue(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	create := func(body map[string]any) {
		recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", body, nil)
		requireStatus(t, recorder, http.StatusCreated)
	}

	create(map[string]any{"title": "Просрочено точно", "priority": "high", "due_at": "2026-11-12T10:00:00Z"})
	create(map[string]any{"title": "В срок точно", "priority": "low", "due_at": "2026-11-13T10:00:00Z"})
	create(map[string]any{"title": "Просрочено календарно", "due_date": "2026-11-11", "tags": []string{"дом"}})
	create(map[string]any{"title": "Сегодня календарно", "due_date": "2026-11-12"})

	recorder, overdue := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks?overdue=true", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	data := overdue["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("overdue len = %d, want 2: %s", len(data), recorder.Body.String())
	}

	recorder, tagged := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks?tags=дом", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(tagged["data"].([]any)) != 1 {
		t.Errorf("tag filter returned %d rows", len(tagged["data"].([]any)))
	}

	recorder, search := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks?search=календарно", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(search["data"].([]any)) != 2 {
		t.Errorf("search returned %d rows", len(search["data"].([]any)))
	}

	recorder, page := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks?limit=1&offset=1&sort=title", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(page["data"].([]any)) != 1 {
		t.Errorf("page len = %d", len(page["data"].([]any)))
	}
	if page["meta"].(map[string]any)["total"] != float64(4) {
		t.Errorf("total = %v", page["meta"].(map[string]any)["total"])
	}

	for _, path := range []string{
		"/api/v1/tasks?limit=0",
		"/api/v1/tasks?limit=101",
		"/api/v1/tasks?offset=-1",
		"/api/v1/tasks?sort=bogus",
		"/api/v1/tasks?priority=urgent",
		"/api/v1/tasks?overdue=maybe",
	} {
		recorder, _ := apiRequest(t, handler, http.MethodGet, path, nil, nil)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, recorder.Code)
		}
	}
}

func TestTaskAPIValidationAndNotFound(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{"title": ""}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "priority": "urgent"}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "due_date": "2026-02-30"}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", "{", nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodGet, "/api/v1/tasks/9999", nil, nil)
	requireStatus(t, recorder, http.StatusNotFound)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks/9999/complete", nil, map[string]string{"If-Match": `"1"`})
	requireStatus(t, recorder, http.StatusNotFound)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks/1/complete", nil, map[string]string{"If-Match": "bad"})
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPatch, "/api/v1/tasks/1", map[string]any{"unknown": true},
		map[string]string{"If-Match": `"1"`})
	requireStatus(t, recorder, http.StatusBadRequest)
}
