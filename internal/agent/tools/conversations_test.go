package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// memoryAssistantStore is an in-memory assistant repository for workspace
// tool tests. It implements just enough of the assistant.Repository
// behaviour the tools rely on: conversations and messages.
type memoryAssistantStore struct {
	mu            sync.Mutex
	conversations map[int64]*domainassistant.Conversation
	messages      map[int64][]*domainassistant.Message
	nextID        int64
}

func newMemoryAssistantStore() *memoryAssistantStore {
	return &memoryAssistantStore{
		conversations: make(map[int64]*domainassistant.Conversation),
		messages:      make(map[int64][]*domainassistant.Message),
	}
}

func (s *memoryAssistantStore) Do(ctx context.Context, fn func(ctx context.Context, tx assistant.Transaction) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(ctx, memoryTransaction{store: s})
}

type memoryTransaction struct{ store *memoryAssistantStore }

func (t memoryTransaction) Assistant() assistant.Repository { return t.store }

func (s *memoryAssistantStore) CreateConversation(ctx context.Context, conversation *domainassistant.Conversation) (int64, error) {
	s.nextID++
	conversation.ID = s.nextID
	s.conversations[conversation.ID] = conversation
	return conversation.ID, nil
}

func (s *memoryAssistantStore) GetConversation(ctx context.Context, id int64) (*domainassistant.Conversation, error) {
	conversation, ok := s.conversations[id]
	if !ok {
		return nil, assistant.ErrConversationNotFound
	}
	return conversation, nil
}

func (s *memoryAssistantStore) ListConversations(ctx context.Context, limit, offset int, kind *domainassistant.Kind) ([]*domainassistant.Conversation, int, error) {
	result := make([]*domainassistant.Conversation, 0, len(s.conversations))
	for _, conversation := range s.conversations {
		if kind != nil && conversation.Kind != *kind {
			continue
		}
		result = append(result, conversation)
	}
	total := len(result)
	if offset < len(result) {
		result = result[offset:]
	} else {
		result = nil
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, total, nil
}

func (s *memoryAssistantStore) TouchConversation(ctx context.Context, id int64, at time.Time) error {
	return nil
}

// BindSourceConversation mirrors the SQL rule: only an unbound project
// conversation can be bound.
func (s *memoryAssistantStore) BindSourceConversation(ctx context.Context, conversationID, sourceID int64) (bool, error) {
	conversation, ok := s.conversations[conversationID]
	if !ok || conversation.Kind != domainassistant.KindProject || conversation.SourceConversationID != nil {
		return false, nil
	}
	conversation.SourceConversationID = &sourceID
	return true, nil
}

func (s *memoryAssistantStore) InsertMessage(ctx context.Context, message *domainassistant.Message) (int64, error) {
	s.messages[message.ConversationID] = append(s.messages[message.ConversationID], message)
	return int64(len(s.messages[message.ConversationID])), nil
}

func (s *memoryAssistantStore) ListMessages(ctx context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error) {
	return s.messages[conversationID], nil
}

func (s *memoryAssistantStore) InsertPendingAction(ctx context.Context, action *domainassistant.PendingAction) (int64, error) {
	return 1, nil
}

func (s *memoryAssistantStore) GetPendingAction(ctx context.Context, id int64) (*domainassistant.PendingAction, error) {
	return nil, assistant.ErrPendingActionNotFound
}

func (s *memoryAssistantStore) ListPendingActions(ctx context.Context, conversationID int64, status *domainassistant.PendingStatus) ([]*domainassistant.PendingAction, error) {
	return nil, nil
}

func (s *memoryAssistantStore) ResolvePendingAction(ctx context.Context, id int64, status domainassistant.PendingStatus, resolution string, at time.Time) error {
	return nil
}

// fakeRunner records delegated turns and returns a fixed reply.
type fakeRunner struct {
	turns []struct {
		conversationID int64
		content        string
	}
	reply string
	err   error
}

func (r *fakeRunner) SendMessageWithInteraction(ctx context.Context, conversationID int64, content string, interaction assistant.InteractionContext) (assistant.SendResult, error) {
	if r.err != nil {
		return assistant.SendResult{}, r.err
	}
	r.turns = append(r.turns, struct {
		conversationID int64
		content        string
	}{conversationID, content})
	return assistant.SendResult{
		ConversationID: conversationID,
		Message:        &domainassistant.Message{ConversationID: conversationID, Role: domainassistant.RoleAssistant, Content: r.reply},
	}, nil
}

func callToolContext(ctx context.Context, t *testing.T, registry *Registry, name string, arguments map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	result, err := registry.Execute(ctx, name, raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return object, nil
}

func workspaceTestRegistry(t *testing.T) (*Registry, *memoryAssistantStore, *fakeRunner) {
	t.Helper()
	store := newMemoryAssistantStore()
	runner := &fakeRunner{reply: "готово: разбор начат"}
	hub := NewWorkspaceHub(store, events.NewBroker())
	hub.Bind(runner)
	registry := NewRegistry()
	registerConversationTools(registry, hub)
	return registry, store, runner
}

func withToolContext(ctx context.Context, conversationID int64, kind domainassistant.Kind, depth int) context.Context {
	return assistant.WithToolContext(ctx, assistant.ToolContext{ConversationID: conversationID, Kind: kind, Depth: depth})
}

func TestCreateWorkspaceStoresProjectConversation(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	result, err := callTool(t, registry, "create_workspace", map[string]any{
		"title":   "Ремонт квартиры",
		"purpose": "Планирование ремонта: смета, подрядчики, сроки",
	})
	if err != nil {
		t.Fatalf("create_workspace: %v", err)
	}
	id := int64(result["workspace_id"].(float64))
	if id == 0 {
		t.Fatalf("workspace id is missing: %+v", result)
	}

	conversation, err := store.GetConversation(context.Background(), id)
	if err != nil {
		t.Fatalf("workspace was not stored: %v", err)
	}
	if conversation.Kind != domainassistant.KindProject {
		t.Fatalf("expected project kind, got %q", conversation.Kind)
	}
	if conversation.Purpose == nil || !strings.Contains(*conversation.Purpose, "смета") {
		t.Fatalf("purpose was not stored: %+v", conversation)
	}
}

func TestDelegateToWorkspaceCreatesRunsAndReuses(t *testing.T) {
	registry, store, runner := workspaceTestRegistry(t)
	ctx := withToolContext(context.Background(), 7, domainassistant.KindMain, 0)

	first, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Диссертация",
		"task":  "Составь план глав по черновику",
	})
	if err != nil {
		t.Fatalf("first delegation: %v", err)
	}
	if first["created"] != true {
		t.Fatalf("expected a new workspace: %+v", first)
	}
	if first["reply"] != "готово: разбор начат" {
		t.Fatalf("reply was not returned: %+v", first)
	}
	if len(runner.turns) != 1 || runner.turns[0].content != "Составь план глав по черновику" {
		t.Fatalf("delegated turn was not run: %+v", runner.turns)
	}

	id := int64(first["workspace_id"].(float64))
	conversation, err := store.GetConversation(context.Background(), id)
	if err != nil {
		t.Fatalf("workspace was not stored: %v", err)
	}
	if conversation.SourceConversationID == nil || *conversation.SourceConversationID != 7 {
		t.Fatalf("source conversation was not recorded: %+v", conversation)
	}

	second, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "диссертация",
		"task":  "Продолжай: глава 2",
	})
	if err != nil {
		t.Fatalf("second delegation: %v", err)
	}
	if second["created"] != false {
		t.Fatalf("existing workspace was reused: %+v", second)
	}
	if int64(second["workspace_id"].(float64)) != id {
		t.Fatalf("workspace id changed: %+v", second)
	}
}

func TestDelegateToWorkspaceRefusedInsideWorkspace(t *testing.T) {
	registry, _, _ := workspaceTestRegistry(t)
	ctx := withToolContext(context.Background(), 3, domainassistant.KindProject, 1)

	if _, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Ещё одно",
		"task":  "вложенное делегирование",
	}); err == nil {
		t.Fatal("nested delegation must be refused")
	}
}

func TestReportResultDeliversToMainChat(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)
	now := time.Now().UTC()
	source := domainassistant.NewConversation("Главный чат", now)
	sourceID, err := store.CreateConversation(context.Background(), source)
	if err != nil {
		t.Fatalf("seed main conversation: %v", err)
	}
	workspace, err := domainassistant.NewWorkspace("Отчёт", "годовой отчёт", &sourceID, now)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	workspaceID, err := store.CreateConversation(context.Background(), workspace)
	if err != nil {
		t.Fatalf("seed workspace conversation: %v", err)
	}

	ctx := withToolContext(context.Background(), workspaceID, domainassistant.KindProject, 0)
	result, err := callToolContext(ctx, t, registry, "report_result", map[string]any{
		"summary": "Смета согласована, следующий шаг — договор с подрядчиком",
	})
	if err != nil {
		t.Fatalf("report_result: %v", err)
	}
	if result["delivered"] != true {
		t.Fatalf("delivery was not confirmed: %+v", result)
	}

	messages, err := store.ListMessages(context.Background(), sourceID, 10)
	if err != nil {
		t.Fatalf("list main messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one handoff message, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Смета согласована") {
		t.Fatalf("summary text is missing: %q", messages[0].Content)
	}
	var metadata struct {
		Handoff struct {
			WorkspaceID int64 `json:"workspace_id"`
		} `json:"handoff"`
	}
	if err := json.Unmarshal(messages[0].Metadata, &metadata); err != nil {
		t.Fatalf("handoff metadata is missing: %v", err)
	}
	if metadata.Handoff.WorkspaceID != workspaceID {
		t.Fatalf("metadata points at the wrong workspace: %+v", metadata)
	}
}

func TestReportResultRefusedInMainChat(t *testing.T) {
	registry, _, _ := workspaceTestRegistry(t)
	ctx := withToolContext(context.Background(), 1, domainassistant.KindMain, 0)

	if _, err := callToolContext(ctx, t, registry, "report_result", map[string]any{
		"summary": "это главный чат",
	}); err == nil {
		t.Fatal("report_result must be refused in the main chat")
	}
}

func TestListWorkspacesReturnsOnlyProjects(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)
	now := time.Now().UTC()
	main := domainassistant.NewConversation("Обычный диалог", now)
	if _, err := store.CreateConversation(context.Background(), main); err != nil {
		t.Fatalf("seed main conversation: %v", err)
	}
	workspace, err := domainassistant.NewWorkspace("Проект", "", nil, now)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := store.CreateConversation(context.Background(), workspace); err != nil {
		t.Fatalf("seed workspace conversation: %v", err)
	}

	result, err := callTool(t, registry, "list_workspaces", nil)
	if err != nil {
		t.Fatalf("list_workspaces: %v", err)
	}
	if result["total"].(float64) != 1 {
		t.Fatalf("expected only the workspace, got %+v", result)
	}
	items := result["workspaces"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Проект" {
		t.Fatalf("unexpected workspace list: %+v", items)
	}
}
