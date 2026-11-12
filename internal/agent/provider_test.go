package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

func TestOpenAIProviderSendsToolsAndParsesToolCalls(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "",
					"tool_calls": [{
						"id": "call-1",
						"type": "function",
						"function": {"name": "create_task", "arguments": "{\"title\":\"x\"}"}
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "test-key", "test-model")
	completion, err := provider.Complete(context.Background(), []assistant.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "создай задачу"},
	}, []assistant.ToolSpec{{
		Name:        "create_task",
		Description: "create",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if captured["model"] != "test-model" {
		t.Errorf("model = %v", captured["model"])
	}
	if captured["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v", captured["tool_choice"])
	}
	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", captured["tools"])
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(completion.ToolCalls))
	}
	call := completion.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "create_task" || !strings.Contains(string(call.Arguments), "title") {
		t.Errorf("call = %+v", call)
	}
}

func TestOpenAIProviderReportsHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "bad", "model")
	if _, err := provider.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("expected an error")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v", err)
	}
}
