package httptransport

import (
	"net/http"
	"testing"
	"time"
)

func TestSettingsHTTP(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodGet, "/api/v1/settings/missing", nil, nil)
	requireStatus(t, recorder, http.StatusNotFound)

	recorder, saved := apiRequest(t, handler, http.MethodPut, "/api/v1/settings/default_priority",
		map[string]any{"value": "high"}, nil)
	requireStatus(t, recorder, http.StatusOK)
	if saved["key"] != "default_priority" {
		t.Errorf("saved = %v", saved)
	}

	recorder, fetched := apiRequest(t, handler, http.MethodGet, "/api/v1/settings/default_priority", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if fetched["value"] != "high" {
		t.Errorf("value = %v", fetched["value"])
	}

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/settings", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	data := list["data"].(map[string]any)
	if data["default_priority"] != "high" {
		t.Errorf("list = %v", data)
	}

	recorder, _ = apiRequest(t, handler, http.MethodPut, "/api/v1/settings/BadKey",
		map[string]any{"value": true}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPut, "/api/v1/settings/theme",
		`{"value": {invalid}}`, nil)
	requireStatus(t, recorder, http.StatusBadRequest)
}

func TestSearchHTTP(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks",
		map[string]any{"title": "Купить молоко"}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/inbox",
		map[string]any{"text": "Молоко закончилось"}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	recorder, result := apiRequest(t, handler, http.MethodGet, "/api/v1/search?q=молоко", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(result["tasks"].([]any)) != 1 {
		t.Errorf("tasks = %v", result["tasks"])
	}
	if len(result["inbox"].([]any)) != 1 {
		t.Errorf("inbox = %v", result["inbox"])
	}
	if result["meta"].(map[string]any)["total"] != float64(2) {
		t.Errorf("meta = %v", result["meta"])
	}

	recorder, _ = apiRequest(t, handler, http.MethodGet, "/api/v1/search?q=", nil, nil)
	requireStatus(t, recorder, http.StatusBadRequest)
}

func TestReadyz(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, body := apiRequest(t, handler, http.MethodGet, "/readyz", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}
