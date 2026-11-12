package httptransport

import (
	"net/http"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/agent/tools"
)

func TestMCPRouteAcceptsBarePath(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	handler.cfg.MCPEnabled = true
	handler.cfg.MCPPath = "/mcp"
	handler.services.Tools = tools.NewRegistry()

	recorder, response := apiRequest(t, handler, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2024-11-05"},
	}, nil)
	requireStatus(t, recorder, http.StatusOK)
	result, ok := response["result"].(map[string]any)
	if !ok || result["protocolVersion"] == nil {
		t.Fatalf("response = %v", response)
	}

	// The trailing-slash form must keep working as well.
	recorder, _ = apiRequest(t, handler, http.MethodPost, "/mcp/", map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "ping",
	}, nil)
	requireStatus(t, recorder, http.StatusOK)
}
