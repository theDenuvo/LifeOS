package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

type scriptedProvider struct {
	responses []assistant.Completion
	calls     int
	last      []assistant.ChatMessage
}

func (p *scriptedProvider) Complete(_ context.Context, messages []assistant.ChatMessage, _ []assistant.ToolSpec) (assistant.Completion, error) {
	p.last = messages
	if p.calls >= len(p.responses) {
		return assistant.Completion{Content: "Готово."}, nil
	}
	response := p.responses[p.calls]
	p.calls++
	return response, nil
}

type fakeTools struct {
	readOnly map[string]bool
	executed []string
	previews map[string]string
}

func (f *fakeTools) Specs() []assistant.ToolSpec {
	return []assistant.ToolSpec{{Name: "list_tasks", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}
}

func (f *fakeTools) IsReadOnly(name string) bool { return f.readOnly[name] }

func (f *fakeTools) Execute(_ context.Context, name string, _ json.RawMessage) (any, error) {
	f.executed = append(f.executed, name)
	return map[string]any{"ok": true, "tool": name}, nil
}

func (f *fakeTools) Preview(name string, _ json.RawMessage) (string, error) {
	if preview, ok := f.previews[name]; ok {
		return preview, nil
	}
	return name, nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func newService(t *testing.T, provider assistant.Provider, tools assistant.ToolRunner) (*assistant.Service, *sqlite.AssistantUnitOfWork) {
	t.Helper()
	return newServiceWithOptions(t, provider, tools)
}

func newServiceWithOptions(t *testing.T, provider assistant.Provider, tools assistant.ToolRunner, opts ...assistant.Option) (*assistant.Service, *sqlite.AssistantUnitOfWork) {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/assistant.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uow := sqlite.NewAssistantUnitOfWork(db)
	service := assistant.NewService(uow, provider, tools, fixedClock{now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}, opts...)
	return service, uow
}

type fakeToday struct{ value summary.Summary }

func (f fakeToday) Today(context.Context) (summary.Summary, error) { return f.value, nil }

type fakeInsights struct{ value []proactive.Insight }

func (f fakeInsights) Compute(context.Context) ([]proactive.Insight, error) { return f.value, nil }

type failingContext struct{}

func (failingContext) Snapshot(context.Context) (string, error) {
	return "", errors.New("context unavailable")
}

func TestSystemPromptIncludesLifeOSContext(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Хорошо."}}}
	tools := &fakeTools{readOnly: map[string]bool{}}
	contextProvider := assistant.NewLifeOSContext(
		fakeToday{value: summary.Summary{
			Stats: summary.Stats{ActiveTotal: 3, OverdueCount: 1, InboxCount: 2},
			FocusTask: &summary.FocusTask{
				ID: 1, Title: "Подготовить отчёт", Time: "14:00", Priority: "high",
			},
		}},
		fakeInsights{value: []proactive.Insight{{
			Key: "overdue", Severity: proactive.SeverityCritical,
			Title: "Просроченные задачи", Detail: "1 задача просрочена",
		}}},
		"Europe/Moscow",
		fixedClock{now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)},
	)
	service, _ := newServiceWithOptions(t, provider, tools, assistant.WithContextProvider(contextProvider))

	if _, err := service.SendMessage(context.Background(), 0, "Что у меня сегодня?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(provider.last) == 0 {
		t.Fatal("provider received no messages")
	}
	prompt := provider.last[0].Content
	for _, want := range []string{
		"Текущее состояние LifeOS",
		"активных 3",
		"«Подготовить отчёт»",
		"Просроченные задачи",
		"уточняющий вопрос",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptMarksUnavailableContext(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Не удалось получить данные."}}}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}}, assistant.WithContextProvider(failingContext{}))

	if _, err := service.SendMessage(context.Background(), 0, "Что у меня на сегодня?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	prompt := provider.last[0].Content
	for _, want := range []string{
		"Контекст LifeOS сейчас недоступен",
		"Не утверждай, что знаешь актуальное состояние",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptIncludesInteractionContext(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Хорошо."}}}
	service, _ := newService(t, provider, &fakeTools{readOnly: map[string]bool{}})

	if _, err := service.SendMessageWithInteraction(context.Background(), 0, "Перенеси её", assistant.InteractionContext{
		Screen: "/tasks", ObjectType: "task", ObjectID: "42", Title: "Подготовить отчёт",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	prompt := provider.last[0].Content
	for _, want := range []string{"/tasks", "task #42", "«Подготовить отчёт»", "проверяй инструментами"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

func TestPendingActionAddsConfirmationGuard(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{ID: "call-1", Name: "create_task", Arguments: json.RawMessage(`{"title":"x"}`)}}},
		{Content: "Готово, задача создана."},
	}}
	service, _ := newService(t, provider, &fakeTools{readOnly: map[string]bool{}, previews: map[string]string{"create_task": "Создать задачу «x»"}})

	result, err := service.SendMessage(context.Background(), 0, "Создай задачу x")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("pending actions = %d, want 1", len(result.PendingActions))
	}
	if !strings.Contains(result.Message.Content, "ждёт вашего подтверждения") {
		t.Errorf("confirmation guard missing: %q", result.Message.Content)
	}
}

func TestSendMessageExecutesReadTools(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{ID: "call-1", Name: "list_tasks", Arguments: json.RawMessage(`{}`)}}},
		{Content: "У вас пока нет задач."},
	}}
	tools := &fakeTools{readOnly: map[string]bool{"list_tasks": true}}
	service, _ := newService(t, provider, tools)

	result, err := service.SendMessage(context.Background(), 0, "Что у меня на сегодня?")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.ConversationID == 0 {
		t.Fatal("conversation was not created")
	}
	if result.Message.Content != "У вас пока нет задач." {
		t.Errorf("content = %q", result.Message.Content)
	}
	if len(result.PendingActions) != 0 {
		t.Errorf("read tool created pending actions: %v", result.PendingActions)
	}
	if len(tools.executed) != 1 || tools.executed[0] != "list_tasks" {
		t.Errorf("executed = %v", tools.executed)
	}
	if len(provider.last) < 2 || provider.last[0].Role != "system" {
		t.Errorf("provider did not receive a system prompt: %v", provider.last)
	}
}

func TestSendMessageCreatesPendingActionAndConfirmExecutes(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-1",
			Name:      "create_task",
			Arguments: json.RawMessage(`{"title":"Купить молоко"}`),
		}}},
		{Content: "Создание задачи ждёт подтверждения."},
	}}
	tools := &fakeTools{
		readOnly: map[string]bool{"list_tasks": true},
		previews: map[string]string{"create_task": "Создать задачу «Купить молоко»"},
	}
	service, _ := newService(t, provider, tools)

	result, err := service.SendMessage(context.Background(), 0, "Создай задачу купить молоко")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("pending actions = %d, want 1", len(result.PendingActions))
	}
	action := result.PendingActions[0]
	if action.Status != domainassistant.PendingStatusPending {
		t.Errorf("status = %q", action.Status)
	}
	if len(tools.executed) != 0 {
		t.Errorf("mutation executed before confirmation: %v", tools.executed)
	}

	confirmed, err := service.ConfirmPendingAction(context.Background(), action.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Action.Status != domainassistant.PendingStatusConfirmed {
		t.Errorf("status = %q", confirmed.Action.Status)
	}
	if len(tools.executed) != 1 || tools.executed[0] != "create_task" {
		t.Errorf("executed = %v", tools.executed)
	}

	if _, err := service.ConfirmPendingAction(context.Background(), action.ID); !errors.Is(err, assistant.ErrPendingActionResolved) {
		t.Errorf("second confirm error = %v, want ErrPendingActionResolved", err)
	}

	view, err := service.GetConversation(context.Background(), result.ConversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(view.Messages) < 4 {
		t.Errorf("messages = %d, want user + tool-call + final + tool result", len(view.Messages))
	}
}

func TestRejectPendingActionDoesNotExecute(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{ID: "call-1", Name: "create_task", Arguments: json.RawMessage(`{"title":"x"}`)}}},
		{Content: "Ждёт подтверждения."},
	}}
	tools := &fakeTools{readOnly: map[string]bool{}}
	service, _ := newService(t, provider, tools)

	result, err := service.SendMessage(context.Background(), 0, "Создай задачу")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	action := result.PendingActions[0]

	rejected, err := service.RejectPendingAction(context.Background(), action.ID)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != domainassistant.PendingStatusRejected {
		t.Errorf("status = %q", rejected.Status)
	}
	if len(tools.executed) != 0 {
		t.Errorf("rejected action executed: %v", tools.executed)
	}
}

func TestSendMessageWithoutProvider(t *testing.T) {
	service, _ := newService(t, nil, &fakeTools{})
	if _, err := service.SendMessage(context.Background(), 0, "Привет"); !errors.Is(err, assistant.ErrProviderNotConfigured) {
		t.Errorf("error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestSendMessageRejectsEmptyContent(t *testing.T) {
	service, _ := newService(t, &scriptedProvider{}, &fakeTools{})
	if _, err := service.SendMessage(context.Background(), 0, "   "); !errors.Is(err, domainassistant.ErrInvalidContent) {
		t.Errorf("error = %v, want ErrInvalidContent", err)
	}
}
