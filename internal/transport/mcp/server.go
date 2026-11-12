// Package mcp implements a small, tools-only Model Context Protocol server over
// Streamable HTTP.
//
// It deliberately reuses the same tool registry as the assistant, so MCP, REST
// and the in-app assistant cannot drift apart. Writes are disabled unless the
// operator opts in: an MCP client has no confirmation UI, so the safe default is
// read-only access.
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/theDenuvo/LifeOS/internal/agent/tools"
)

const protocolVersion = "2024-11-05"

// Server handles JSON-RPC requests from MCP clients.
type Server struct {
	registry    *tools.Registry
	allowWrites bool
	version     string
}

// NewServer builds an MCP server over a tool registry.
func NewServer(registry *tools.Registry, allowWrites bool, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{registry: registry, allowWrites: allowWrites, version: version}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeHTTP handles POST /mcp.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}

	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil || request.Method == "" {
		writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}

	// Notifications carry no id and expect no response body.
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch request.Method {
	case "initialize":
		writeRPC(w, http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{"name": "lifeos", "version": s.version},
			},
		})
	case "ping":
		writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
	case "tools/list":
		writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": s.listTools()}})
	case "tools/call":
		writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: s.callTool(r.Context(), request.Params)})
	default:
		writeRPC(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})
	}
}

func (s *Server) listTools() []map[string]any {
	specs := s.registry.Specs()
	result := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		if !s.allowWrites && !s.registry.IsReadOnly(spec.Name) {
			continue
		}
		result = append(result, map[string]any{
			"name":        spec.Name,
			"description": spec.Description,
			"inputSchema": spec.Parameters,
		})
	}
	return result
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) map[string]any {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil || call.Name == "" {
		return toolError("invalid tools/call parameters")
	}
	if !s.allowWrites && !s.registry.IsReadOnly(call.Name) {
		return toolError("writes are disabled for MCP; set LIFEOS_MCP_ALLOW_WRITES=true to enable them")
	}

	result, err := s.registry.Execute(ctx, call.Name, call.Arguments)
	if err != nil {
		return toolError(err.Error())
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return toolError("tool result is not serialisable")
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(payload)}},
		"isError": false,
	}
}

func toolError(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": message}},
		"isError": true,
	}
}

func writeRPC(w http.ResponseWriter, status int, response rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
