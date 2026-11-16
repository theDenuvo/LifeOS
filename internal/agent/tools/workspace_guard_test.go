package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

func seedWorkspace(t *testing.T, store *memoryAssistantStore, title string, sourceID *int64) *domainassistant.Conversation {
	t.Helper()
	workspace, err := domainassistant.NewWorkspace(title, "тема", sourceID, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	workspace.ID, err = store.CreateConversation(context.Background(), workspace)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return workspace
}

// Depth 0 does not mean the user is in the main chat: a workspace opened
// directly also runs its turn at depth 0. Delegation from it must be refused
// and must not bind the route to the project chat.
func TestDelegateToWorkspaceRefusedFromDirectlyOpenedWorkspace(t *testing.T) {
	registry, store, runner := workspaceTestRegistry(t)

	// An existing workspace the user opens directly (no main chat involved).
	workspace := seedWorkspace(t, store, "Прямо открытая тема", nil)

	// The turn inside the workspace: depth 0, kind project.
	ctx := withToolContext(context.Background(), workspace.ID, domainassistant.KindProject, 0)
	if _, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Вложенная тема",
		"task":  "вложенное делегирование из открытого пространства",
	}); err == nil {
		t.Fatal("delegation from a directly opened workspace must be refused")
	}

	// Nothing was delegated and nothing was created: the refusal happens
	// before any store access.
	if len(runner.turns) != 0 {
		t.Fatalf("delegated turn ran anyway: %+v", runner.turns)
	}
	if len(store.conversations) != 1 {
		t.Fatalf("a nested workspace was created: %d conversations", len(store.conversations))
	}
	reloaded, err := store.GetConversation(context.Background(), workspace.ID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if reloaded.SourceConversationID != nil {
		t.Fatalf("route must not point at the project chat itself, got %v", *reloaded.SourceConversationID)
	}
}

// Creating a workspace from inside a directly opened workspace must be
// refused as well: such a space would carry a project chat as its route.
func TestCreateWorkspaceRefusedFromDirectlyOpenedWorkspace(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	workspace := seedWorkspace(t, store, "Открытое пространство", nil)

	ctx := withToolContext(context.Background(), workspace.ID, domainassistant.KindProject, 0)
	if _, err := callToolContext(ctx, t, registry, "create_workspace", map[string]any{
		"title":   "Вложенное пространство",
		"purpose": "создано из проектного чата",
	}); err == nil {
		t.Fatal("create_workspace from a project conversation must be refused")
	}
	if len(store.conversations) != 1 {
		t.Fatalf("a nested workspace was created: %d conversations", len(store.conversations))
	}
}

// The error wording tells the model where delegation is allowed.
func TestDelegateRefusalExplainsMainChatRequirement(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	workspace := seedWorkspace(t, store, "Тема", nil)

	ctx := withToolContext(context.Background(), workspace.ID, domainassistant.KindProject, 0)
	_, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Другая тема",
		"task":  "что угодно",
	})
	if err == nil || !strings.Contains(err.Error(), "только из главного чата") {
		t.Fatalf("error = %v, want a main-chat requirement message", err)
	}
}
