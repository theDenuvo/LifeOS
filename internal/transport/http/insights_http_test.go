package httptransport

import (
	"net/http"
	"testing"
	"time"
)

func TestInsightsHTTPDismissAndSnooze(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{
		"title":  "Просрочено",
		"due_at": "2026-11-11T10:00:00Z",
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{
		"title":    "Сегодня",
		"due_date": "2026-11-12",
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/insights", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	keys := insightKeys(list)
	if !keys["overdue"] || !keys["due-today"] {
		t.Fatalf("insights = %v", keys)
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/insights/overdue/dismiss", nil, nil)
	requireStatus(t, recorder, http.StatusNoContent)

	recorder, list = apiRequest(t, handler, http.MethodGet, "/api/v1/insights", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if insightKeys(list)["overdue"] {
		t.Error("dismissed insight is still returned")
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/insights/due-today/snooze",
		map[string]any{"until": "2026-11-13T12:00:00Z"}, nil)
	requireStatus(t, recorder, http.StatusNoContent)

	recorder, list = apiRequest(t, handler, http.MethodGet, "/api/v1/insights", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if insightKeys(list)["due-today"] {
		t.Error("snoozed insight is still returned")
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/insights/due-today/snooze",
		map[string]any{"until": "not-a-date"}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)
}

func insightKeys(response map[string]any) map[string]bool {
	keys := make(map[string]bool)
	data, _ := response["data"].([]any)
	for _, item := range data {
		if entry, ok := item.(map[string]any); ok {
			if key, ok := entry["key"].(string); ok {
				keys[key] = true
			}
		}
	}
	return keys
}
