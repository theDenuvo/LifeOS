// Package agent contains the LLM provider implementations.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

// OpenAIProvider talks to any OpenAI-compatible chat-completions endpoint:
// OpenRouter, Ollama's /v1 API, or a local gateway.
type OpenAIProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAIProvider builds a provider. An empty API key is allowed for local
// endpoints such as Ollama.
func NewOpenAIProvider(baseURL, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 90 * time.Second},
	}
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	Tools      []chatTool    `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one completion request.
func (p *OpenAIProvider) Complete(ctx context.Context, messages []assistant.ChatMessage, tools []assistant.ToolSpec) (assistant.Completion, error) {
	request := chatRequest{
		Model:    p.model,
		Messages: make([]chatMessage, 0, len(messages)),
	}
	for _, message := range messages {
		converted := chatMessage{
			Role:       message.Role,
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		for _, call := range message.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, chatToolCall{
				ID:   call.ID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      call.Name,
					Arguments: string(call.Arguments),
				},
			})
		}
		request.Messages = append(request.Messages, converted)
	}
	for _, tool := range tools {
		request.Tools = append(request.Tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	if len(request.Tools) > 0 {
		request.ToolChoice = "auto"
	}

	body, err := json.Marshal(request)
	if err != nil {
		return assistant.Completion{}, fmt.Errorf("encode llm request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return assistant.Completion{}, fmt.Errorf("build llm request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return assistant.Completion{}, fmt.Errorf("llm request: %w", err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return assistant.Completion{}, fmt.Errorf("read llm response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return assistant.Completion{}, fmt.Errorf("llm: status %d: %s", response.StatusCode, truncate(string(payload), 500))
	}

	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return assistant.Completion{}, fmt.Errorf("decode llm response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return assistant.Completion{}, fmt.Errorf("llm: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return assistant.Completion{}, fmt.Errorf("llm: empty response")
	}

	message := decoded.Choices[0].Message
	completion := assistant.Completion{Content: message.Content}
	for _, call := range message.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if len(arguments) == 0 || !json.Valid(arguments) {
			arguments = json.RawMessage(`{}`)
		}
		completion.ToolCalls = append(completion.ToolCalls, assistant.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: arguments,
		})
	}
	return completion, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
