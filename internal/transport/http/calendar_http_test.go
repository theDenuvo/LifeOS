package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func performRequest(t *testing.T, handler *Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, req)
	return recorder
}

func TestCalendarEventLifecycle(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	created := performRequest(t, handler, http.MethodPost, "/api/v1/calendar/events", `{
		"title":"Синк с командой",
		"calendar":"work",
		"starts_at":"2026-11-17T10:00:00+03:00",
		"ends_at":"2026-11-17T11:00:00+03:00",
		"location":"Zoom"
	}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	if etag := created.Header().Get("ETag"); etag != `"1"` {
		t.Fatalf("create event: expected ETag 1, got %q", etag)
	}
	var createdBody eventBody
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatalf("decode created event: %v", err)
	}

	patched := performRequest(t, handler, http.MethodPatch, "/api/v1/calendar/events/1", `{
		"title":"Синк с командой (перенос)",
		"starts_at":"2026-11-17T12:00:00+03:00",
		"ends_at":"2026-11-17T13:00:00+03:00"
	}`, map[string]string{"If-Match": `"1"`})
	if patched.Code != http.StatusOK {
		t.Fatalf("patch event: expected 200, got %d: %s", patched.Code, patched.Body.String())
	}
	if etag := patched.Header().Get("ETag"); etag != `"2"` {
		t.Fatalf("patch event: expected ETag 2, got %q", etag)
	}

	deleted := performRequest(t, handler, http.MethodDelete, "/api/v1/calendar/events/1", "", map[string]string{"If-Match": `"2"`})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete event: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}

	missing := performRequest(t, handler, http.MethodGet, "/api/v1/calendar/events/1", "", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("get deleted event: expected 404, got %d", missing.Code)
	}
}

func TestCalendarReturnsEventsTasksAndConflicts(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	for _, body := range []string{
		`{"title":"Синк 1","calendar":"work","starts_at":"2026-11-17T10:00:00+03:00","ends_at":"2026-11-17T11:00:00+03:00"}`,
		`{"title":"Синк 2","calendar":"meetings","starts_at":"2026-11-17T10:30:00+03:00","ends_at":"2026-11-17T11:30:00+03:00"}`,
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/v1/calendar/events", body, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("create overlapping event: %d: %s", response.Code, response.Body.String())
		}
	}

	task := performRequest(t, handler, http.MethodPost, "/api/v1/tasks", `{
		"title":"Задача с точным временем",
		"priority":"high",
		"due_at":"2026-11-17T10:45:00+03:00"
	}`, nil)
	if task.Code != http.StatusCreated {
		t.Fatalf("create task: %d: %s", task.Code, task.Body.String())
	}

	response := performRequest(t, handler, http.MethodGet, "/api/v1/calendar?from=2026-11-01&to=2026-11-30", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("get calendar: %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		From      string         `json:"from"`
		To        string         `json:"to"`
		Events    []eventBody    `json:"events"`
		Tasks     []taskBody     `json:"tasks"`
		Conflicts []conflictBody `json:"conflicts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode calendar: %v", err)
	}
	if body.From != "2026-11-01" || body.To != "2026-11-30" {
		t.Errorf("unexpected range: %s..%s", body.From, body.To)
	}
	if len(body.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(body.Events))
	}
	if len(body.Tasks) != 1 || body.Tasks[0].Title != "Задача с точным временем" {
		t.Errorf("expected the task in the calendar, got %+v", body.Tasks)
	}
	if len(body.Conflicts) < 2 {
		t.Errorf("expected event-event and event-task conflicts, got %d", len(body.Conflicts))
	}
}

func TestProfileAndStatus(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	profile := performRequest(t, handler, http.MethodGet, "/api/v1/profile", "", nil)
	if profile.Code != http.StatusOK {
		t.Fatalf("get profile: %d: %s", profile.Code, profile.Body.String())
	}
	var profileBody struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Initials string `json:"initials"`
	}
	if err := json.Unmarshal(profile.Body.Bytes(), &profileBody); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profileBody.Name != "Пользователь" || profileBody.Initials != "П" {
		t.Errorf("unexpected default profile: %+v", profileBody)
	}

	updated := performRequest(t, handler, http.MethodPut, "/api/v1/profile", `{"name":"Анна Ковалёва","role":"Продуктовый дизайнер"}`, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("put profile: %d: %s", updated.Code, updated.Body.String())
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &profileBody); err != nil {
		t.Fatalf("decode updated profile: %v", err)
	}
	if profileBody.Name != "Анна Ковалёва" || profileBody.Initials != "АК" {
		t.Errorf("unexpected updated profile: %+v", profileBody)
	}

	status := performRequest(t, handler, http.MethodGet, "/api/v1/status", "", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("get status: %d: %s", status.Code, status.Body.String())
	}
	var statusBody struct {
		OK       bool   `json:"ok"`
		Version  string `json:"version"`
		Database struct {
			OK bool `json:"ok"`
		} `json:"database"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !statusBody.OK || !statusBody.Database.OK || statusBody.Version != "test" {
		t.Errorf("unexpected status: %+v", statusBody)
	}
}

func TestEventValidationAndPrecondition(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	invalid := performRequest(t, handler, http.MethodPost, "/api/v1/calendar/events", `{
		"title":"Некорректное событие",
		"starts_at":"2026-11-17T11:00:00+03:00",
		"ends_at":"2026-11-17T10:00:00+03:00"
	}`, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for inverted range, got %d", invalid.Code)
	}

	noVersion := performRequest(t, handler, http.MethodPatch, "/api/v1/calendar/events/1", `{"title":"Без версии"}`, nil)
	if noVersion.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 without If-Match, got %d", noVersion.Code)
	}
}
