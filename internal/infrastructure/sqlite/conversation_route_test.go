package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// The database itself refuses a workspace route that does not point at a main
// conversation, so report_result can never be aimed at a project chat even by
// a write that bypasses the tools.
func TestConversationSourceMustReferenceMainChat(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	uow := NewAssistantUnitOfWork(db)
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	var mainID, projectID int64
	if err := uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		var err error
		mainID, err = tx.Assistant().CreateConversation(ctx, domainassistant.NewConversation("Главный чат", now))
		if err != nil {
			return err
		}
		// Created without a route (as from the interface), then bound by the
		// first delegation — the legitimate path.
		workspace, err := domainassistant.NewWorkspace("Пространство", "тема", nil, now)
		if err != nil {
			return err
		}
		projectID, err = tx.Assistant().CreateConversation(ctx, workspace)
		if err != nil {
			return err
		}
		set, err := tx.Assistant().BindSourceConversation(ctx, projectID, mainID)
		if err != nil || !set {
			t.Fatalf("BindSourceConversation = %v, %v", set, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed conversations: %v", err)
	}

	// An update aiming the route at another project conversation must abort.
	err := uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		other, err := domainassistant.NewWorkspace("Другое пространство", "", nil, now)
		if err != nil {
			return err
		}
		otherID, err := tx.Assistant().CreateConversation(ctx, other)
		if err != nil {
			return err
		}
		_, err = tx.Assistant().BindSourceConversation(ctx, otherID, projectID)
		return err
	})
	if err == nil {
		t.Fatal("binding a route to a project conversation must fail")
	}

	// A plain UPDATE bypassing the repository must abort as well.
	if _, err := db.ExecContext(ctx,
		`UPDATE conversations SET source_conversation_id = ? WHERE id = ?`,
		projectID, mainID); err == nil {
		t.Fatal("raw update must refuse a project conversation as source")
	}

	// A workspace without a route stays untouched by the guard.
	if err := uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		workspace, err := domainassistant.NewWorkspace("Без маршрута", "", nil, now)
		if err != nil {
			return err
		}
		if _, err := tx.Assistant().CreateConversation(ctx, workspace); err != nil {
			return err
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unbound workspace insert must succeed: %v", err)
	}
}
