package assistant

import (
	"errors"
	"testing"
	"time"
)

func TestNewWorkspaceBuildsProjectConversation(t *testing.T) {
	source := int64(3)
	workspace, err := NewWorkspace("  Ремонт  ", "  смета и подрядчики ", &source, time.Now())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if workspace.Kind != KindProject {
		t.Fatalf("kind = %q, want project", workspace.Kind)
	}
	if workspace.Title == nil || *workspace.Title != "Ремонт" {
		t.Fatalf("title = %v", workspace.Title)
	}
	if workspace.Purpose == nil || *workspace.Purpose != "смета и подрядчики" {
		t.Fatalf("purpose = %v", workspace.Purpose)
	}
	if workspace.SourceConversationID == nil || *workspace.SourceConversationID != 3 {
		t.Fatalf("source = %v", workspace.SourceConversationID)
	}
}

func TestNewWorkspaceWithoutPurpose(t *testing.T) {
	workspace, err := NewWorkspace("Проект", "", nil, time.Now())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if workspace.Purpose != nil {
		t.Fatalf("empty purpose must stay unset, got %q", *workspace.Purpose)
	}
	if workspace.SourceConversationID != nil {
		t.Fatal("nil source must stay unset")
	}
}

func TestNewWorkspaceRejectsEmptyTitle(t *testing.T) {
	if _, err := NewWorkspace("   ", "purpose", nil, time.Now()); !errors.Is(err, ErrEmptyWorkspaceTitle) {
		t.Fatalf("err = %v, want ErrEmptyWorkspaceTitle", err)
	}
}

func TestNewConversationIsMainChat(t *testing.T) {
	conversation := NewConversation("Первый вопрос", time.Now())
	if conversation.Kind != KindMain {
		t.Fatalf("kind = %q, want main", conversation.Kind)
	}
	if !conversation.Kind.IsValid() {
		t.Fatal("main kind must be valid")
	}
}
