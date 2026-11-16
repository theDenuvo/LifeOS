package tools

import (
	"context"
	"strings"
	"testing"

	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// The full routing scenario: a workspace is created from the interface (no
// source binding), the main chat later delegates into it, the first
// delegation binds the route, and report_result delivers the result back
// into that main conversation.
func TestUIWorkspaceBindsOnFirstDelegationAndReportsBack(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	// The user creates a workspace from the sidebar: no source conversation.
	created, err := callTool(t, registry, "create_workspace", map[string]any{
		"title":   "Диссертация",
		"purpose": "план глав",
	})
	if err != nil {
		t.Fatalf("create_workspace: %v", err)
	}
	workspaceID := int64(created["workspace_id"].(float64))
	workspace, err := store.GetConversation(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("workspace was not stored: %v", err)
	}
	if workspace.SourceConversationID != nil {
		t.Fatalf("UI-created workspace must start unbound, got %v", *workspace.SourceConversationID)
	}

	// The user asks the assistant (main chat #7) to continue the work there.
	ctx := withToolContext(context.Background(), 7, domainassistant.KindMain, 0)
	delegated, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Диссертация",
		"task":  "Продолжи: план глав",
	})
	if err != nil {
		t.Fatalf("delegate_to_workspace: %v", err)
	}
	if delegated["created"] != false {
		t.Fatalf("existing workspace must be reused, got %+v", delegated)
	}

	workspace, err = store.GetConversation(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if workspace.SourceConversationID == nil || *workspace.SourceConversationID != 7 {
		t.Fatalf("first delegation must bind the route to main chat 7, got %v", workspace.SourceConversationID)
	}

	// Inside the workspace the assistant reports a significant result; it must
	// land in the main chat that delegated the work.
	workCtx := withToolContext(context.Background(), workspaceID, domainassistant.KindProject, 0)
	reported, err := callToolContext(workCtx, t, registry, "report_result", map[string]any{
		"summary": "План глав утверждён",
	})
	if err != nil {
		t.Fatalf("report_result: %v", err)
	}
	if reported["delivered"] != true {
		t.Fatalf("delivery was not confirmed: %+v", reported)
	}

	messages, err := store.ListMessages(context.Background(), 7, 10)
	if err != nil {
		t.Fatalf("list main messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "План глав утверждён") {
		t.Fatalf("handoff must land in main chat 7, got %d message(s)", len(messages))
	}
}

// The route belongs to the chat that delegated first: a later delegation from
// another main conversation must not rebind it.
func TestWorkspaceRouteBindingIsNotOverwritten(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	ctx := withToolContext(context.Background(), 7, domainassistant.KindMain, 0)
	first, err := callToolContext(ctx, t, registry, "delegate_to_workspace", map[string]any{
		"title": "Проект А",
		"task":  "начать",
	})
	if err != nil {
		t.Fatalf("first delegation: %v", err)
	}
	workspaceID := int64(first["workspace_id"].(float64))

	// Another main conversation delegates into the same workspace by title.
	other := withToolContext(context.Background(), 42, domainassistant.KindMain, 0)
	second, err := callToolContext(other, t, registry, "delegate_to_workspace", map[string]any{
		"title": "проект а",
		"task":  "продолжить",
	})
	if err != nil {
		t.Fatalf("second delegation: %v", err)
	}
	if int64(second["workspace_id"].(float64)) != workspaceID {
		t.Fatalf("delegation by title must reuse the same workspace: %+v", second)
	}

	workspace, err := store.GetConversation(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if workspace.SourceConversationID == nil || *workspace.SourceConversationID != 7 {
		t.Fatalf("binding must stay with the first delegating chat, got %v", workspace.SourceConversationID)
	}
}

// A workspace without a route must refuse report_result instead of pretending
// the result was delivered: no message anywhere, the model sees an error.
func TestReportResultWithoutRouteFailsHonestly(t *testing.T) {
	registry, store, _ := workspaceTestRegistry(t)

	// Created by the interface, never delegated from a main chat.
	created, err := callTool(t, registry, "create_workspace", map[string]any{
		"title":   "Одиночное пространство",
		"purpose": "локальная тема",
	})
	if err != nil {
		t.Fatalf("create_workspace: %v", err)
	}
	workspaceID := int64(created["workspace_id"].(float64))

	// mainID 0 would mean "no running conversation"; the honest answer is the
	// same either way: an error, not a delivered=true confirmation.
	workCtx := withToolContext(context.Background(), workspaceID, domainassistant.KindProject, 0)
	if _, err := callToolContext(workCtx, t, registry, "report_result", map[string]any{
		"summary": "итог без маршрута",
	}); err == nil {
		t.Fatal("report_result must fail without a route, not report success")
	}

	for conversationID := range store.conversations {
		messages, _ := store.ListMessages(context.Background(), conversationID, 10)
		for _, message := range messages {
			if strings.Contains(message.Content, "итог без маршрута") {
				t.Fatalf("unrouted result must not be written anywhere, found in conversation %d", conversationID)
			}
		}
	}
}
