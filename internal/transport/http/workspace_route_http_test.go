package httptransport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

// The full stage-3 routing scenario over the real stack: the user creates a
// workspace from the interface, the main chat later delegates into it by
// title, the first delegation binds the route, and report_result inside the
// workspace delivers the result back into that main conversation.
func TestWorkspaceRouteFromUIToMainChatAndBack(t *testing.T) {
	// Round 1 (main chat): delegate by title into the UI-created workspace.
	// Round 2 (workspace, delegated turn): report the result, then answer.
	// Round 3 (main chat): wrap up.
	provider := &httpScriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-delegate",
			Name:      "delegate_to_workspace",
			Arguments: json.RawMessage(`{"title":"Диссертация","task":"Продолжи работу: план глав"}`),
		}}},
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-report",
			Name:      "report_result",
			Arguments: json.RawMessage(`{"summary":"План глав утверждён"}`),
		}}},
		{Content: "Итог передан в главный чат."},
		{Content: "Работа передана, итог получен."},
	}}
	handler, cleanup := newWorkspaceAPI(t, provider)
	defer cleanup()

	// The user creates the workspace from the sidebar: no source binding.
	recorder, workspace := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/workspaces", map[string]any{
		"title":   "Диссертация",
		"purpose": "план глав",
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	workspaceID := int64(workspace["id"].(float64))
	if workspace["source_conversation_id"] != nil {
		t.Fatalf("UI-created workspace must start unbound: %v", workspace["source_conversation_id"])
	}

	// The main chat (created by this call) asks to continue the work there.
	recorder, main := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{
		"content": "Продолжи разбор диссертации в пространстве",
	}, nil)
	requireStatus(t, recorder, http.StatusOK)
	mainID := int64(main["conversation_id"].(float64))

	// The delegated turn ran inside the workspace and reported its result.
	recorder, view := apiRequest(t, handler, http.MethodGet,
		"/api/v1/assistant/conversations/"+strconv.FormatInt(workspaceID, 10), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	reported := false
	for _, item := range view["messages"].([]any) {
		msg := item.(map[string]any)
		if msg["role"] == "assistant" && msg["content"] == "Итог передан в главный чат." {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("delegated turn did not report the result: %v", view["messages"])
	}

	// The workspace is now bound to the delegating main chat.
	if view["source_conversation_id"].(float64) != float64(mainID) {
		t.Fatalf("source_conversation_id = %v, want %v", view["source_conversation_id"], mainID)
	}

	// The result landed in the main chat as a badged handoff message.
	recorder, mainView := apiRequest(t, handler, http.MethodGet,
		"/api/v1/assistant/conversations/"+strconv.FormatInt(mainID, 10), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	handoff := false
	for _, item := range mainView["messages"].([]any) {
		msg := item.(map[string]any)
		if msg["role"] != "assistant" || !strings.Contains(msg["content"].(string), "План глав утверждён") {
			continue
		}
		metadata, _ := msg["metadata"].(map[string]any)
		if metadata != nil {
			if _, ok := metadata["handoff"].(map[string]any); ok {
				handoff = true
			}
		}
	}
	if !handoff {
		t.Fatalf("handoff message with the result is missing in the main chat: %v", mainView["messages"])
	}

	// A second main conversation delegates into the same workspace: the
	// binding must stay with the chat that delegated first.
	provider.responses = append(provider.responses,
		assistant.Completion{ToolCalls: []assistant.ToolCall{{
			ID:        "call-delegate-2",
			Name:      "delegate_to_workspace",
			Arguments: json.RawMessage(`{"workspace_id":` + strconv.FormatInt(workspaceID, 10) + `,"task":"ещё шаг"}`),
		}}},
		assistant.Completion{Content: "принято в пространстве."},
		assistant.Completion{Content: "работа продолжена."},
	)
	recorder, secondMain := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{
		"content": "Займёмся диссертацией и отсюда",
	}, nil)
	requireStatus(t, recorder, http.StatusOK)
	secondMainID := int64(secondMain["conversation_id"].(float64))
	if secondMainID == mainID {
		t.Fatal("the second turn must open its own main conversation")
	}

	recorder, view = apiRequest(t, handler, http.MethodGet,
		"/api/v1/assistant/conversations/"+strconv.FormatInt(workspaceID, 10), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if view["source_conversation_id"].(float64) != float64(mainID) {
		t.Fatalf("binding was overwritten: source = %v, want %v", view["source_conversation_id"], mainID)
	}
}
