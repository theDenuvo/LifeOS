package httptransport

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInboxAPICaptureListConvertDismiss(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, captured := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{
		"text": "Позвонить маме про воскресенье",
		"meta": map[string]any{"source": "voice"},
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	if captured["text"] != "Позвонить маме про воскресенье" {
		t.Errorf("text = %v", captured["text"])
	}
	if captured["processed_at"] != nil {
		t.Errorf("processed_at = %v, want null", captured["processed_at"])
	}
	id := int64(captured["id"].(float64))

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/inbox", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if list["meta"].(map[string]any)["total"] != float64(1) {
		t.Errorf("inbox total = %v", list["meta"].(map[string]any)["total"])
	}

	recorder, converted := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/convert", id), map[string]any{
		"priority": "high",
		"due_date": "2026-11-13",
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	if converted["title"] != "Позвонить маме про воскресенье" {
		t.Errorf("converted title = %v", converted["title"])
	}
	if converted["priority"] != "high" || converted["due_date"] != "2026-11-13" {
		t.Errorf("converted = %v / %v", converted["priority"], converted["due_date"])
	}
	if recorder.Header().Get("ETag") != `"1"` {
		t.Errorf("ETag = %q", recorder.Header().Get("ETag"))
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/convert", id), nil, nil)
	requireStatus(t, recorder, http.StatusConflict)

	recorder, unprocessed := apiRequest(t, handler, http.MethodGet, "/api/v1/inbox", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if unprocessed["meta"].(map[string]any)["total"] != float64(0) {
		t.Errorf("unprocessed total = %v", unprocessed["meta"].(map[string]any)["total"])
	}

	recorder, all := apiRequest(t, handler, http.MethodGet, "/api/v1/inbox?unprocessed_only=false", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	items := all["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("all items = %d", len(items))
	}
	item := items[0].(map[string]any)
	if item["processed_at"] == nil || item["converted_task_id"] == nil {
		t.Errorf("processed item = %v", item)
	}

	recorder, _ = apiRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/inbox/%d", id), nil, nil)
	requireStatus(t, recorder, http.StatusConflict)

	recorder, second := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": "Удалить меня"}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	secondID := int64(second["id"].(float64))

	recorder, _ = apiRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/inbox/%d", secondID), nil, nil)
	requireStatus(t, recorder, http.StatusNoContent)

	recorder, _ = apiRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/inbox/%d", secondID), nil, nil)
	requireStatus(t, recorder, http.StatusNotFound)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/inbox/9999/convert", nil, nil)
	requireStatus(t, recorder, http.StatusNotFound)
}

func TestInboxAPIFailedConversionLeavesItemUnprocessed(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, captured := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": "Не потерять"}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	id := int64(captured["id"].(float64))

	recorder, _ = apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/convert", id),
		map[string]any{"priority": "urgent"}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/inbox", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if list["meta"].(map[string]any)["total"] != float64(1) {
		t.Errorf("failed conversion consumed the item: %v", list["meta"])
	}

	recorder, tasks := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if tasks["meta"].(map[string]any)["total"] != float64(0) {
		t.Errorf("failed conversion created a task: %v", tasks["meta"])
	}
}

func TestInboxAPILongCaptureKeepsFullText(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	longText := strings.Repeat("я", 600)
	recorder, captured := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": longText}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	id := int64(captured["id"].(float64))

	recorder, converted := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/convert", id), nil, nil)
	requireStatus(t, recorder, http.StatusCreated)

	title := converted["title"].(string)
	if len([]rune(title)) != 500 {
		t.Errorf("title runes = %d, want 500", len([]rune(title)))
	}
	note, _ := converted["note"].(string)
	if note != longText {
		t.Errorf("note lost captured text: %d runes", len([]rune(note)))
	}
}

func TestInboxAPIValidation(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": ""}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": strings.Repeat("x", 2001)}, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodGet, "/api/v1/inbox?limit=0", nil, nil)
	requireStatus(t, recorder, http.StatusBadRequest)

	recorder, _ = apiRequest(t, handler, http.MethodGet, "/api/v1/inbox?unprocessed_only=maybe", nil, nil)
	requireStatus(t, recorder, http.StatusBadRequest)
}

func TestSummaryToday(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	create := func(body map[string]any) map[string]any {
		recorder, created := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", body, nil)
		requireStatus(t, recorder, http.StatusCreated)
		return created
	}

	create(map[string]any{"title": "Просрочено", "priority": "high", "due_at": "2026-11-12T10:00:00Z"})
	create(map[string]any{"title": "Сегодня вечером", "priority": "high", "due_at": "2026-11-12T17:00:00Z"})
	create(map[string]any{"title": "Сегодня без времени", "due_date": "2026-11-12"})
	done := create(map[string]any{"title": "Сделано сегодня", "due_date": "2026-11-12"})

	recorder, _ := apiRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", int64(done["id"].(float64))), nil,
		map[string]string{"If-Match": `"1"`})
	requireStatus(t, recorder, http.StatusOK)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": "Мысль"}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	recorder, summary := apiRequest(t, handler, http.MethodGet, "/api/v1/summary/today", nil, nil)
	requireStatus(t, recorder, http.StatusOK)

	stats := summary["stats"].(map[string]any)
	if stats["active_total"] != float64(3) {
		t.Errorf("active_total = %v, want 3", stats["active_total"])
	}
	if stats["overdue_count"] != float64(1) {
		t.Errorf("overdue_count = %v, want 1", stats["overdue_count"])
	}
	if stats["high_priority_count"] != float64(2) {
		t.Errorf("high_priority_count = %v, want 2", stats["high_priority_count"])
	}
	if stats["completed_today"] != float64(1) {
		t.Errorf("completed_today = %v, want 1", stats["completed_today"])
	}
	if stats["inbox_count"] != float64(1) {
		t.Errorf("inbox_count = %v, want 1", stats["inbox_count"])
	}

	today := summary["today"].([]any)
	if len(today) != 3 {
		t.Errorf("today len = %d, want 3: %v", len(today), today)
	}
	overdue := summary["overdue"].([]any)
	if len(overdue) != 1 {
		t.Errorf("overdue len = %d, want 1", len(overdue))
	}
	if overdue[0].(map[string]any)["days_overdue"] != float64(1) {
		t.Errorf("days_overdue = %v", overdue[0].(map[string]any)["days_overdue"])
	}

	focus := summary["focus_task"].(map[string]any)
	if focus["title"] != "Сегодня вечером" {
		t.Errorf("focus = %v, want the high-priority task scheduled today", focus["title"])
	}
}
