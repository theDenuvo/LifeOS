package sqlite

import (
	"context"
	"testing"
	"time"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

func TestDocumentRepositoryStoresProposalsWithDates(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	uow := NewDocumentUnitOfWork(db)
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	var documentID int64
	err := uow.Do(context.Background(), func(ctx context.Context, tx appdocuments.Transaction) error {
		value := &document.Document{
			Filename:  "plan.docx",
			MIME:      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Size:      100,
			SHA256:    "hash",
			Path:      "/tmp/plan.docx",
			Status:    document.StatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		}
		id, err := tx.Documents().CreateDocument(ctx, value)
		if err != nil {
			return err
		}
		documentID = id
		return nil
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	due, err := task.ParseCalendarDate("2026-11-20")
	if err != nil {
		t.Fatal(err)
	}
	duePtr := &due
	proposal, err := document.NewProposedTask(documentID, "Подготовить отчёт", nil, duePtr, task.PriorityHigh, 0.9, nil, now)
	if err != nil {
		t.Fatal(err)
	}

	err = uow.Do(context.Background(), func(ctx context.Context, tx appdocuments.Transaction) error {
		return tx.Documents().ReplaceProposals(ctx, documentID, []*document.ProposedTask{proposal}, "text", now)
	})
	if err != nil {
		t.Fatalf("replace proposals: %v", err)
	}

	err = uow.Do(context.Background(), func(ctx context.Context, tx appdocuments.Transaction) error {
		found, err := tx.Documents().ListProposals(ctx, documentID)
		if err != nil {
			return err
		}
		if len(found) != 1 {
			t.Fatalf("expected one proposal, got %d", len(found))
		}
		if found[0].DueDate == nil || found[0].DueDate.String() != "2026-11-20" {
			t.Fatalf("unexpected due date: %+v", found[0].DueDate)
		}
		value, err := tx.Documents().GetDocument(ctx, documentID)
		if err != nil {
			return err
		}
		if value.Status != document.StatusProcessed {
			t.Fatalf("expected processed, got %s", value.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
}
