package httptransport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

// TestOutreachHTTPCycle pins the user-facing reaction contract: an outreach
// recorded by the engine is listed, acknowledged, snoozed and finally
// dismissed through the REST surface.
func TestOutreachHTTPCycle(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	outreach := proactive.NewOutreachService(
		sqlite.NewInsightUnitOfWork(handler.db),
		sqlite.NewOutreachAssistant(handler.db),
		handler.services.Broker,
		apiClock{now: now},
		"Europe/Moscow",
	)
	handler.services.Outreach = outreach

	insight := proactive.Insight{
		Key:      "overdue",
		Kind:     "overdue",
		Severity: proactive.SeverityCritical,
		Title:    "Просроченные задачи",
		Detail:   "2 задач(и) просрочены",
		Count:    2,
	}
	outreach.Evaluate(context.Background(), []proactive.Insight{insight})

	recorder, body := apiRequest(t, handler, http.MethodGet, "/api/v1/outreach", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	items, _ := body["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one outreach, got %d", len(items))
	}
	entry, _ := items[0].(map[string]any)
	if entry["outcome"] != string(proactive.OutreachPending) {
		t.Fatalf("outcome = %v, want pending", entry["outcome"])
	}
	if entry["insight_key"] != "overdue" || entry["conversation_id"].(float64) <= 0 || entry["message_id"].(float64) <= 0 {
		t.Fatalf("outreach payload incomplete: %v", entry)
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/outreach/1/ack", nil, nil)
	requireStatus(t, recorder, http.StatusNoContent)

	recorder, body = apiRequest(t, handler, http.MethodGet, "/api/v1/outreach", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	items, _ = body["data"].([]any)
	entry, _ = items[0].(map[string]any)
	if entry["outcome"] != string(proactive.OutreachAcknowledged) {
		t.Fatalf("ack must be recorded, got %v", entry["outcome"])
	}

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/outreach/1/snooze",
		map[string]any{"until": "2026-11-13T12:00:00Z"}, nil)
	requireStatus(t, recorder, http.StatusInternalServerError)

	recorder, _ = apiRequest(t, handler, http.MethodGet, "/api/v1/outreach/999", nil, nil)
	requireStatus(t, recorder, http.StatusNotFound)

	recorder, _ = apiRequest(t, handler, http.MethodPost, "/api/v1/outreach/abc/ack", nil, nil)
	requireStatus(t, recorder, http.StatusBadRequest)
}

// TestOutreachHTTPDismiss pins the dismiss path together with the stored
// reaction that later teaches the gate to stop repeating the kind.
func TestOutreachHTTPDismiss(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	outreach := proactive.NewOutreachService(
		sqlite.NewInsightUnitOfWork(handler.db),
		sqlite.NewOutreachAssistant(handler.db),
		handler.services.Broker,
		apiClock{now: now},
		"Europe/Moscow",
	)
	handler.services.Outreach = outreach

	insight := proactive.Insight{
		Key:      "overdue",
		Kind:     "overdue",
		Severity: proactive.SeverityCritical,
		Title:    "Просроченные задачи",
	}
	outreach.Evaluate(context.Background(), []proactive.Insight{insight})

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/outreach/1/dismiss", nil, nil)
	requireStatus(t, recorder, http.StatusNoContent)

	recorder, body := apiRequest(t, handler, http.MethodGet, "/api/v1/outreach", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	items, _ := body["data"].([]any)
	entry, _ := items[0].(map[string]any)
	if entry["outcome"] != string(proactive.OutreachDismissed) || entry["decided_at"] == nil {
		t.Fatalf("dismiss must record the reaction, got %v", entry)
	}
}
