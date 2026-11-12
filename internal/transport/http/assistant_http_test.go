package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

type httpScriptedProvider struct {
	responses []assistant.Completion
	calls     int
}

func (p *httpScriptedProvider) Complete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec) (assistant.Completion, error) {
	if p.calls >= len(p.responses) {
		return assistant.Completion{Content: "Готово."}, nil
	}
	response := p.responses[p.calls]
	p.calls++
	return response, nil
}

func TestAssistantHTTPPendingActionFlow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	provider := &httpScriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-1",
			Name:      "create_task",
			Arguments: json.RawMessage(`{"title":"Купить молоко","priority":"high"}`),
		}}},
		{Content: "Создание задачи ждёт подтверждения."},
	}}
	handler, cleanup := newTaskAPIWithProvider(t, now, provider)
	defer cleanup()

	recorder, response := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{
		"content": "Создай задачу купить молоко",
	}, nil)
	requireStatus(t, recorder, http.StatusOK)

	conversationID := int64(response["conversation_id"].(float64))
	message := response["message"].(map[string]any)
	if message["content"] != "Создание задачи ждёт подтверждения." {
		t.Errorf("message = %v", message["content"])
	}
	pending := response["pending_actions"].([]any)
	if len(pending) != 1 {
		t.Fatalf("pending actions = %d", len(pending))
	}
	action := pending[0].(map[string]any)
	if action["tool_name"] != "create_task" || action["status"] != "pending" {
		t.Errorf("action = %v", action)
	}
	preview := action["preview"].(map[string]any)
	if preview["summary"] != "Создать задачу «Купить молоко»" {
		t.Errorf("preview = %v", preview)
	}
	actionID := int64(action["id"].(float64))

	// Nothing is written before confirmation.
	recorder, tasks := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if tasks["meta"].(map[string]any)["total"] != float64(0) {
		t.Errorf("task created before confirmation: %v", tasks["meta"])
	}

	recorder, confirmed := apiRequest(t, handler, http.MethodPost,
		fmt.Sprintf("/api/v1/assistant/pending-actions/%d/confirm", actionID), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if confirmed["pending_action"].(map[string]any)["status"] != "confirmed" {
		t.Errorf("confirmed = %v", confirmed)
	}
	result := confirmed["result"].(map[string]any)
	if result["title"] != "Купить молоко" {
		t.Errorf("result = %v", result)
	}

	recorder, tasks = apiRequest(t, handler, http.MethodGet, "/api/v1/tasks", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if tasks["meta"].(map[string]any)["total"] != float64(1) {
		t.Errorf("task not created after confirmation: %v", tasks["meta"])
	}

	recorder, conversation := apiRequest(t, handler, http.MethodGet,
		fmt.Sprintf("/api/v1/assistant/conversations/%d", conversationID), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(conversation["messages"].([]any)) < 4 {
		t.Errorf("conversation messages = %d", len(conversation["messages"].([]any)))
	}
}

func TestAssistantHTTPRejectFlow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	provider := &httpScriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{ID: "call-1", Name: "create_task", Arguments: json.RawMessage(`{"title":"Не создавать"}`)}}},
		{Content: "Ждёт подтверждения."},
	}}
	handler, cleanup := newTaskAPIWithProvider(t, now, provider)
	defer cleanup()

	recorder, response := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{"content": "Создай"}, nil)
	requireStatus(t, recorder, http.StatusOK)
	actionID := int64(response["pending_actions"].([]any)[0].(map[string]any)["id"].(float64))

	recorder, rejected := apiRequest(t, handler, http.MethodPost,
		fmt.Sprintf("/api/v1/assistant/pending-actions/%d/reject", actionID), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if rejected["pending_action"].(map[string]any)["status"] != "rejected" {
		t.Errorf("rejected = %v", rejected)
	}

	recorder, tasks := apiRequest(t, handler, http.MethodGet, "/api/v1/tasks", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if tasks["meta"].(map[string]any)["total"] != float64(0) {
		t.Errorf("rejected action created a task: %v", tasks["meta"])
	}
}

func TestAssistantHTTPWithoutProvider(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{"content": "Привет"}, nil)
	requireStatus(t, recorder, http.StatusServiceUnavailable)
}
