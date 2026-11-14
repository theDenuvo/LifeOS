package assistant_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

type streamingProvider struct {
	responses []assistant.Completion
	calls     int
}

func (p *streamingProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	return assistant.Completion{}, nil
}

func (p *streamingProvider) StreamComplete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	if p.calls >= len(p.responses) {
		return assistant.Completion{Content: "Готово."}, nil
	}
	response := p.responses[p.calls]
	p.calls++
	if response.Reasoning != "" && onDelta != nil {
		if err := onDelta(assistant.StreamDelta{Reasoning: response.Reasoning}); err != nil {
			return assistant.Completion{}, err
		}
	}
	if response.Content != "" && onDelta != nil {
		if err := onDelta(assistant.StreamDelta{Content: response.Content}); err != nil {
			return assistant.Completion{}, err
		}
	}
	return response, nil
}

func TestStreamAutoConfirmExecutesMutation(t *testing.T) {
	provider := &streamingProvider{responses: []assistant.Completion{
		{
			Reasoning: "Нужно создать задачу.",
			ToolCalls: []assistant.ToolCall{{
				ID: "call-1", Name: "create_task",
				Arguments: json.RawMessage(`{"title":"Купить хлеб"}`),
			}},
		},
		{Content: "Задача создана."},
	}}
	tools := &fakeTools{
		readOnly: map[string]bool{"create_task": false},
		previews: map[string]string{"create_task": "Создать задачу «Купить хлеб»"},
	}
	service, _ := newService(t, provider, tools)

	var events []assistant.StreamEvent
	result, err := service.SendMessageStream(context.Background(), 0, "Создай задачу", assistant.InteractionContext{}, "openrouter/free", true, func(event assistant.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(tools.executed) != 1 || tools.executed[0] != "create_task" {
		t.Fatalf("expected auto-confirmed mutation, executed=%v", tools.executed)
	}
	if len(result.PendingActions) != 0 {
		t.Fatalf("auto-confirm must not leave pending actions: %+v", result.PendingActions)
	}
	if result.Message.Content != "Задача создана." {
		t.Fatalf("unexpected final message: %q", result.Message.Content)
	}
	var sawDone, sawTool, sawReasoning bool
	for _, event := range events {
		if event.Type == "done" {
			sawDone = true
		}
		if event.Type == "tool" {
			sawTool = true
		}
		if event.Type == "reasoning" {
			sawReasoning = true
		}
	}
	if !sawDone || !sawTool || !sawReasoning {
		t.Fatalf("missing stream events: %+v", events)
	}
}

func TestStreamWithoutAutoConfirmLeavesPending(t *testing.T) {
	provider := &streamingProvider{responses: []assistant.Completion{
		{
			ToolCalls: []assistant.ToolCall{{
				ID: "call-1", Name: "create_task",
				Arguments: json.RawMessage(`{"title":"Купить хлеб"}`),
			}},
		},
		{Content: "Жду подтверждения."},
	}}
	tools := &fakeTools{
		readOnly: map[string]bool{"create_task": false},
		previews: map[string]string{"create_task": "Создать задачу «Купить хлеб»"},
	}
	service, _ := newService(t, provider, tools)

	result, err := service.SendMessageStream(context.Background(), 0, "Создай задачу", assistant.InteractionContext{}, "", false, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(tools.executed) != 0 {
		t.Fatalf("mutation must wait for confirmation, executed=%v", tools.executed)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("expected one pending action, got %+v", result.PendingActions)
	}
}
