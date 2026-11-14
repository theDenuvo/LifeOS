package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// StreamComplete performs an OpenAI-compatible streaming completion. Text and
// reasoning deltas are delivered as they arrive; tool-call deltas are
// accumulated and returned as a normal Completion.
func (p *OpenAIProvider) StreamComplete(ctx context.Context, messages []assistant.ChatMessage, tools []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	request := chatRequest{
		Model:         p.model,
		Messages:      make([]chatMessage, 0, len(messages)),
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	for _, message := range messages {
		converted := chatMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, chatToolCall{
				ID: call.ID, Type: "function",
				Function: chatFunctionCall{Name: call.Name, Arguments: string(call.Arguments)},
			})
		}
		request.Messages = append(request.Messages, converted)
	}
	for _, tool := range tools {
		request.Tools = append(request.Tools, chatTool{Type: "function", Function: chatFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}})
	}
	if len(request.Tools) > 0 {
		request.ToolChoice = "auto"
	}

	body, err := json.Marshal(request)
	if err != nil {
		return assistant.Completion{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return assistant.Completion{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return assistant.Completion{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return assistant.Completion{}, &ProviderError{Status: response.StatusCode, Body: truncate(string(payload), 600)}
	}

	type toolAccumulator struct {
		ID        string
		Name      string
		Arguments strings.Builder
	}
	toolCalls := map[int]*toolAccumulator{}
	var content, reasoning strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return assistant.Completion{}, &ProviderError{Body: chunk.Error.Message, Transient: transientMessage(chunk.Error.Message)}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		reasoningDelta := delta.Reasoning
		if reasoningDelta == "" {
			reasoningDelta = delta.ReasoningContent
		}
		if reasoningDelta != "" {
			reasoning.WriteString(reasoningDelta)
			if onDelta != nil {
				if err := onDelta(assistant.StreamDelta{Reasoning: reasoningDelta}); err != nil {
					return assistant.Completion{}, err
				}
			}
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				if err := onDelta(assistant.StreamDelta{Content: delta.Content}); err != nil {
					return assistant.Completion{}, err
				}
			}
		}
		for _, call := range delta.ToolCalls {
			index := call.Index
			accumulator := toolCalls[index]
			if accumulator == nil {
				accumulator = &toolAccumulator{}
				toolCalls[index] = accumulator
			}
			if call.ID != "" {
				accumulator.ID = call.ID
			}
			if call.Function.Name != "" {
				accumulator.Name = call.Function.Name
			}
			if call.Function.Arguments != "" {
				accumulator.Arguments.WriteString(call.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return assistant.Completion{}, err
	}

	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	completion := assistant.Completion{Content: content.String(), Reasoning: reasoning.String()}
	for _, index := range indexes {
		accumulator := toolCalls[index]
		if accumulator.Name == "" {
			continue
		}
		arguments := json.RawMessage(accumulator.Arguments.String())
		if len(arguments) == 0 || !json.Valid(arguments) {
			arguments = json.RawMessage(`{}`)
		}
		completion.ToolCalls = append(completion.ToolCalls, assistant.ToolCall{
			ID: accumulator.ID, Name: accumulator.Name, Arguments: arguments,
		})
	}
	return completion, nil
}

// transientMessage reports whether an in-stream provider error describes a
// temporary condition (rate limit, overload, upstream hiccup) rather than a
// broken request.
func transientMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"rate limit", "rate-limit", "ratelimit", "too many requests", "overloaded",
		"temporarily", "temporary", "timeout", "timed out", "unavailable",
		"try again", "429", "500", "502", "503", "504",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
