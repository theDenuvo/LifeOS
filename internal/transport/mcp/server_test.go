package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/agent/tools"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

func testRegistry(calls *[]string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.Tool{
		Spec: assistant.ToolSpec{
			Name:        "list_tasks",
			Description: "read tasks",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
		ReadOnly: true,
		Execute: func(_ context.Context, _ json.RawMessage) (any, error) {
			*calls = append(*calls, "list_tasks")
			return map[string]any{"tasks": []any{}}, nil
		},
	})
	registry.Register(tools.Tool{
		Spec: assistant.ToolSpec{
			Name:        "create_task",
			Description: "create a task",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`),
		},
		Execute: func(_ context.Context, _ json.RawMessage) (any, error) {
			*calls = append(*calls, "create_task")
			return map[string]any{"id": 1, "title": "x"}, nil
		},
	})
	return registry
}

func rpc(t *testing.T, server http.Handler, method string, params any, withID bool) map[string]any {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if withID {
		payload["id"] = 1
	}
	if params != nil {
		payload["params"] = params
	}
	encoded, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(encoded))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("%s: status %d: %s", method, recorder.Code, recorder.Body.String())
	}
	if !withID {
		return nil
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("%s: decode: %v", method, err)
	}
	return response
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	calls := make([]string, 0)
	server := NewServer(testRegistry(&calls), false, "test")

	init := rpc(t, server, "initialize", map[string]any{"protocolVersion": "2024-11-05"}, true)
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocol = %v", result["protocolVersion"])
	}

	list := rpc(t, server, "tools/list", nil, true)
	available := list["result"].(map[string]any)["tools"].([]any)
	if len(available) != 1 {
		t.Fatalf("read-only mode exposed %d tools, want 1", len(available))
	}
	if available[0].(map[string]any)["name"] != "list_tasks" {
		t.Errorf("tool = %v", available[0])
	}
}

func TestMCPCallsReadToolAndBlocksWritesByDefault(t *testing.T) {
	calls := make([]string, 0)
	server := NewServer(testRegistry(&calls), false, "test")

	read := rpc(t, server, "tools/call", map[string]any{"name": "list_tasks", "arguments": map[string]any{}}, true)
	result := read["result"].(map[string]any)
	if result["isError"] != false {
		t.Errorf("read result = %v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), "tasks") {
		t.Errorf("content = %v", content)
	}

	write := rpc(t, server, "tools/call", map[string]any{"name": "create_task", "arguments": map[string]any{"title": "x"}}, true)
	writeResult := write["result"].(map[string]any)
	if writeResult["isError"] != true {
		t.Errorf("write was not blocked: %v", writeResult)
	}
	text := writeResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "disabled") {
		t.Errorf("message = %q", text)
	}
	if len(calls) != 1 || calls[0] != "list_tasks" {
		t.Errorf("calls = %v", calls)
	}
}

func TestMCPWritesWhenEnabled(t *testing.T) {
	calls := make([]string, 0)
	server := NewServer(testRegistry(&calls), true, "test")

	list := rpc(t, server, "tools/list", nil, true)
	if len(list["result"].(map[string]any)["tools"].([]any)) != 2 {
		t.Fatal("write tool not exposed")
	}

	write := rpc(t, server, "tools/call", map[string]any{"name": "create_task", "arguments": map[string]any{"title": "x"}}, true)
	result := write["result"].(map[string]any)
	if result["isError"] != false {
		t.Errorf("write failed: %v", result)
	}
	if len(calls) != 1 || calls[0] != "create_task" {
		t.Errorf("calls = %v", calls)
	}
}

func TestMCPNotificationsAndMethods(t *testing.T) {
	calls := make([]string, 0)
	server := NewServer(testRegistry(&calls), false, "test")

	rpc(t, server, "notifications/initialized", nil, false)

	unknown := rpc(t, server, "resources/list", nil, true)
	errorPayload := unknown["error"].(map[string]any)
	if errorPayload["code"] != float64(-32601) {
		t.Errorf("error = %v", errorPayload)
	}

	request := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d", recorder.Code)
	}
}
