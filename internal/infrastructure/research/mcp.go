package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// mcpProtocolVersion is the Streamable HTTP revision LifeOS speaks. Servers
// answer with their own version and LifeOS keeps using the session, so a newer
// server still works.
const mcpProtocolVersion = "2024-11-05"

// mcpEnvelope is one JSON-RPC 2.0 answer.
type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// callMCP talks to a tools-only MCP server: it opens a session, then either
// lists the tools or calls one. tools/list is the default operation, which is
// what the interface's "check" button and the assistant use first.
func (c *Client) callMCP(ctx context.Context, connection *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	endpoint := strings.TrimSpace(connection.Endpoint)
	if endpoint == "" {
		return appresearch.CallResult{}, appresearch.ErrInvalidCall
	}
	started := time.Now()
	sessionID, err := c.mcpInitialize(ctx, endpoint)
	if err != nil {
		return appresearch.CallResult{}, err
	}
	operation := strings.TrimSpace(request.Operation)
	var (
		result  json.RawMessage
		callErr error
	)
	switch operation {
	case "", "tools/list":
		result, callErr = c.mcpRequest(ctx, endpoint, sessionID, "tools/list", map[string]any{})
	default:
		arguments := map[string]any{}
		if body := strings.TrimSpace(request.Body); body != "" {
			if err := json.Unmarshal([]byte(body), &arguments); err != nil {
				return appresearch.CallResult{}, fmt.Errorf("%w: аргументы инструмента MCP должны быть JSON-объектом", appresearch.ErrInvalidCall)
			}
		}
		if query := strings.TrimSpace(request.Query); query != "" {
			if _, exists := arguments["query"]; !exists {
				arguments["query"] = query
			}
		}
		result, callErr = c.mcpRequest(ctx, endpoint, sessionID, "tools/call", map[string]any{
			"name":      operation,
			"arguments": arguments,
		})
	}
	if callErr != nil {
		return appresearch.CallResult{}, callErr
	}
	return appresearch.CallResult{
		Status:      http.StatusOK,
		ContentType: "application/json",
		Body:        string(result),
		Duration:    time.Since(started),
	}, nil
}

// mcpInitialize opens a Streamable HTTP session and returns its id.
func (c *Client) mcpInitialize(ctx context.Context, endpoint string) (string, error) {
	answer, headers, err := c.mcpPost(ctx, endpoint, "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "LifeOS", "version": "1.0"},
		},
	})
	if err != nil {
		return "", err
	}
	if answer.Error != nil {
		return "", fmt.Errorf("MCP-сервер отказал в подключении: %s", answer.Error.Message)
	}
	sessionID := headers.Get("Mcp-Session-Id")
	// The notification is best effort: a server that does not require it
	// answers 202, and one that does not understand it still serves calls.
	if _, _, err := c.mcpPost(ctx, endpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}); err != nil {
		return sessionID, nil
	}
	return sessionID, nil
}

// mcpRequest performs one request inside an existing session.
func (c *Client) mcpRequest(ctx context.Context, endpoint, sessionID, method string, params map[string]any) (json.RawMessage, error) {
	answer, _, err := c.mcpPost(ctx, endpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	if answer.Error != nil {
		return nil, fmt.Errorf("MCP-сервер вернул ошибку %d: %s", answer.Error.Code, answer.Error.Message)
	}
	if len(answer.Result) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return answer.Result, nil
}

// mcpPost sends one JSON-RPC message and decodes the answer, whether it comes
// back as JSON or as a single event-stream frame.
func (c *Client) mcpPost(ctx context.Context, endpoint, sessionID string, message map[string]any) (*mcpEnvelope, http.Header, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("User-Agent", c.userAgent)
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP-сервер недоступен: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, defaultMaxCallBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("ответ MCP не прочитан: %w", err)
	}
	if response.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0 {
		// A notification: no answer body by definition.
		return &mcpEnvelope{}, response.Header, nil
	}
	if response.StatusCode >= 400 {
		return nil, response.Header, fmt.Errorf("MCP-сервер ответил %d: %s", response.StatusCode, truncate(string(body), 300))
	}
	envelope, err := decodeMCPBody(body, response.Header.Get("Content-Type"))
	if err != nil {
		return nil, response.Header, err
	}
	return envelope, response.Header, nil
}

// decodeMCPBody reads a JSON answer or the matching frame of an event stream.
func decodeMCPBody(body []byte, contentType string) (*mcpEnvelope, error) {
	if !strings.Contains(strings.ToLower(contentType), "event-stream") {
		var envelope mcpEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("ответ MCP не разобран: %w", err)
		}
		return &envelope, nil
	}
	for _, frame := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(frame)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var envelope mcpEnvelope
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if len(envelope.Result) > 0 || envelope.Error != nil {
			return &envelope, nil
		}
	}
	return nil, fmt.Errorf("MCP-сервер не прислал ответ")
}
