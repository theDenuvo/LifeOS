package assistant_test

import (
	"context"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// flakyProvider fails the first completion, then answers.
type flakyProvider struct {
	failing   bool
	answer    string
	reasoning string
	stream    bool
	calls     int
}

func (p *flakyProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	p.calls++
	if p.failing && p.calls == 1 {
		return assistant.Completion{}, &testProviderError{}
	}
	return assistant.Completion{Content: p.answer}, nil
}

func (p *flakyProvider) StreamComplete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	p.calls++
	if p.failing && p.calls == 1 {
		if p.reasoning != "" && onDelta != nil {
			_ = onDelta(assistant.StreamDelta{Reasoning: p.reasoning})
		}
		return assistant.Completion{}, &testProviderError{}
	}
	if onDelta != nil && p.answer != "" {
		_ = onDelta(assistant.StreamDelta{Content: p.answer})
	}
	return assistant.Completion{Content: p.answer}, nil
}

type testProviderError struct{}

func (testProviderError) Error() string { return "llm: status 503: upstream unavailable" }

func collectStreamEvents(service *assistant.Service, conversationID int64, content string) ([]assistant.StreamEvent, error) {
	events := make([]assistant.StreamEvent, 0, 8)
	_, err := service.SendMessageStream(context.Background(), conversationID, content, assistant.InteractionContext{}, "", true, func(event assistant.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func TestRetryAfterFailedTurnKeepsOneUserMessage(t *testing.T) {
	provider := &flakyProvider{failing: true, answer: "Готово."}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}})

	result, err := service.SendMessageWithInteraction(context.Background(), 0, "поставь задачу на завтра", assistant.InteractionContext{})
	if err == nil {
		t.Fatal("the first attempt must fail")
	}
	if result.ConversationID != 0 {
		t.Fatalf("a failed turn must not report a conversation: %+v", result)
	}

	// The user retries the same message in the same conversation.
	conversations, _, err := service.ListConversations(context.Background(), 1, 0, nil)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("conversation must exist after the user message was stored: %v", err)
	}
	conversationID := conversations[0].ID

	if _, err := service.SendMessageWithInteraction(context.Background(), conversationID, "поставь задачу на завтра", assistant.InteractionContext{}); err != nil {
		t.Fatalf("retry must succeed: %v", err)
	}

	after, err := service.GetConversation(context.Background(), conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	userMessages := 0
	for _, message := range after.Messages {
		if message.Role == domainassistant.RoleUser {
			userMessages++
			if strings.TrimSpace(message.Content) != "поставь задачу на завтра" {
				t.Fatalf("unexpected user content: %q", message.Content)
			}
		}
	}
	if userMessages != 1 {
		t.Fatalf("retry must reuse the stored user message, got %d copies", userMessages)
	}
}

func TestFailedStreamDoesNotPersistEmptyAssistantMessage(t *testing.T) {
	provider := &flakyProvider{failing: true, reasoning: "думаю про расписание"}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}})

	events, err := collectStreamEvents(service, 0, "разбери расписание")
	if err == nil {
		t.Fatal("expected the stream to fail")
	}

	var conversationID int64
	for _, event := range events {
		if event.Type == "start" {
			if data, ok := event.Data.(map[string]any); ok {
				conversationID, _ = data["conversation_id"].(int64)
			}
		}
	}
	if conversationID == 0 {
		t.Fatal("start event with conversation id expected")
	}

	view, err := service.GetConversation(context.Background(), conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	for _, message := range view.Messages {
		if message.Role == domainassistant.RoleAssistant && strings.TrimSpace(message.Content) == "" {
			t.Fatalf("failed turn left an empty assistant message: %+v", message)
		}
	}

	errorMessage := ""
	for _, event := range events {
		if event.Type == "error" {
			if data, ok := event.Data.(map[string]any); ok {
				errorMessage, _ = data["message"].(string)
			}
		}
	}
	if !strings.Contains(errorMessage, "Повторите") {
		t.Fatalf("stream error must tell the user to retry, got %q", errorMessage)
	}
}

func TestStreamRetryEventsAreForwarded(t *testing.T) {
	provider := &retryReportProvider{}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}})

	events, err := collectStreamEvents(service, 0, "привет")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == "retry" {
			found = true
		}
	}
	if !found {
		t.Fatalf("retry events must reach the workspace: %+v", events)
	}
}

// retryReportProvider reports one retry through the observer, then answers.
type retryReportProvider struct{}

func (retryReportProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	return assistant.Completion{Content: "ok"}, nil
}

func (retryReportProvider) StreamComplete(ctx context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	return assistant.Completion{Content: "ok"}, nil
}

func (retryReportProvider) WithModel(string) assistant.Provider { return retryReportProvider{} }

func (retryReportProvider) WithRetryObserver(observer func(assistant.ProviderRetry)) assistant.Provider {
	observer(assistant.ProviderRetry{Model: "nvidia/nemotron:free", Attempt: 1, Reason: "rate limited"})
	return retryReportProvider{}
}
