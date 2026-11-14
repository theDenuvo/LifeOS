package documents_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
	infradocuments "github.com/theDenuvo/LifeOS/internal/infrastructure/documents"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

type fakeExtractor struct{}

func (fakeExtractor) Extract(context.Context, string, string, string) (appdocuments.Extraction, error) {
	return appdocuments.Extraction{Text: "Задача: подготовить отчёт к 20 ноября"}, nil
}

type fakeMarkdownConverter struct{}

func (fakeMarkdownConverter) ConvertToMarkdown(context.Context, appdocuments.TaskExtractionInput) (string, error) {
	return "# Документ. Подготовить отчёт к 20 ноября.", nil
}

func TestDocumentServiceUploadProcessStoresMarkdown(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/documents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	store, err := infradocuments.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	taskService := tasks.NewService(sqlite.NewTaskUnitOfWork(db), nil)
	service := appdocuments.NewService(
		sqlite.NewDocumentUnitOfWork(db),
		store,
		fakeExtractor{},
		fakeMarkdownConverter{},
		taskService,
		nil,
		"Europe/Moscow",
	)

	uploaded, err := service.Upload(context.Background(), "plan.txt", "text/plain", bytes.NewBufferString("Задача"), 7)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if uploaded.Status != document.StatusPending {
		t.Fatalf("unexpected upload status: %s", uploaded.Status)
	}
	if err := service.Process(context.Background(), uploaded.ID); err != nil {
		t.Fatalf("process: %v", err)
	}
	view, err := service.Get(context.Background(), uploaded.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Document.Status != document.StatusProcessed {
		t.Fatalf("unexpected document status: %s", view.Document.Status)
	}
	if view.Document.ExtractedText == nil || !strings.Contains(*view.Document.ExtractedText, "подготовить отчёт") {
		t.Fatalf("markdown was not stored: %+v", view.Document.ExtractedText)
	}
	if len(view.Tasks) != 0 {
		t.Fatalf("conversion must not create task proposals: %+v", view.Tasks)
	}
}
