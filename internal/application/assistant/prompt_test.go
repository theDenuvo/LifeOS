package assistant_test

import (
	"context"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

// capturingProvider records the messages each turn sends to the model, so
// tests can assert on the system prompt itself.
type capturingProvider struct {
	messages  []assistant.ChatMessage
	responses []assistant.Completion
}

func (p *capturingProvider) Complete(_ context.Context, messages []assistant.ChatMessage, _ []assistant.ToolSpec) (assistant.Completion, error) {
	p.messages = messages
	if len(p.responses) == 0 {
		return assistant.Completion{Content: "Готово."}, nil
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

func (p *capturingProvider) systemPrompt() string {
	for _, message := range p.messages {
		if message.Role == "system" {
			return message.Content
		}
	}
	return ""
}

// The routing rules are part of the main chat's system prompt even when no
// workspace exists yet — otherwise the model could not create the first one
// or offer moving a grown topic.
func TestMainChatPromptCarriesRoutingWithoutWorkspaces(t *testing.T) {
	provider := &capturingProvider{}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{})

	if _, err := service.SendMessageWithInteraction(context.Background(), 0, "Привет", assistant.InteractionContext{}); err != nil {
		t.Fatalf("send message: %v", err)
	}

	prompt := provider.systemPrompt()
	if !strings.Contains(prompt, "create_workspace") {
		t.Fatalf("routing instructions missing create_workspace:\n%s", prompt)
	}
	if !strings.Contains(prompt, "delegate_to_workspace") {
		t.Fatalf("routing instructions missing delegate_to_workspace:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Результаты пространств возвращаются в главный чат сами") {
		t.Fatalf("routing instructions missing result return:\n%s", prompt)
	}
	if strings.Contains(prompt, "Открытые рабочие пространства") {
		t.Fatalf("no workspaces exist, the list block must be absent:\n%s", prompt)
	}
}

// With workspaces present the rules stay in place and the list travels
// separately.
func TestMainChatPromptKeepsRoutingWithWorkspaces(t *testing.T) {
	provider := &capturingProvider{}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{})

	if _, err := service.CreateWorkspace(context.Background(), "Диссертация", "план глав", nil); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if _, err := service.SendMessageWithInteraction(context.Background(), 0, "Привет", assistant.InteractionContext{}); err != nil {
		t.Fatalf("send message: %v", err)
	}

	prompt := provider.systemPrompt()
	if !strings.Contains(prompt, "create_workspace") || !strings.Contains(prompt, "delegate_to_workspace") {
		t.Fatalf("routing instructions must survive existing workspaces:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Открытые рабочие пространства") {
		t.Fatalf("workspace list block missing:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Диссертация") {
		t.Fatalf("workspace list must name the workspace:\n%s", prompt)
	}
}

// A project workspace whose route back to the main chat is known advertises
// report_result; one without a route warns the model instead of promising a
// delivery that cannot happen.
func TestProjectPromptReflectsReturnRoute(t *testing.T) {
	probe := func(t *testing.T, sourceID *int64, title string) string {
		t.Helper()
		provider := &capturingProvider{}
		service, _ := newServiceWithOptions(t, provider, &fakeTools{})
		workspace, err := service.CreateWorkspace(context.Background(), title, "тема", sourceID)
		if err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		if _, err := service.SendMessageWithInteraction(context.Background(), workspace.ID, "Продолжаем", assistant.InteractionContext{}); err != nil {
			t.Fatalf("send message in workspace: %v", err)
		}
		return provider.systemPrompt()
	}

	source := int64(7)
	route := probe(t, &source, "С маршрутом")
	if !strings.Contains(route, "report_result") {
		t.Fatalf("bound workspace prompt must advertise report_result:\n%s", route)
	}

	noRoute := probe(t, nil, "Без маршрута")
	if strings.Contains(noRoute, "report_result с кратким итогом") {
		t.Fatalf("unbound workspace prompt must not promise report_result delivery:\n%s", noRoute)
	}
	if !strings.Contains(noRoute, "нет привязки к главному чату") {
		t.Fatalf("unbound workspace prompt must warn about the missing route:\n%s", noRoute)
	}
}
