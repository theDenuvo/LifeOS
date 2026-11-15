package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domaindocument "github.com/theDenuvo/LifeOS/internal/domain/document"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

type memoryTestClock struct{ at time.Time }

func (c memoryTestClock) Now() time.Time { return c.at }

func newMemoryService(t *testing.T) (*appmemory.Service, func()) {
	t.Helper()
	service, _, cleanup := newMemoryServiceWithDB(t)
	return service, cleanup
}

func newMemoryServiceWithDB(t *testing.T) (*appmemory.Service, *sql.DB, func()) {
	t.Helper()
	db := setupTestDB(t)
	clock := memoryTestClock{at: time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)}
	service := appmemory.NewService(NewMemoryUnitOfWork(db), clock)
	return service, db, func() { db.Close() }
}

func userFact(title, content string) appmemory.CreateInput {
	confidence := 1.0
	return appmemory.CreateInput{
		Title: title, Content: content, Category: domainmemory.CategoryPreference,
		Certainty: domainmemory.CertaintyFact, Confidence: &confidence,
		Source: domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Слова пользователя"},
	}
}

func TestMemoryPersistsProvenanceAndDeduplicates(t *testing.T) {
	service, cleanup := newMemoryService(t)
	defer cleanup()
	ctx := context.Background()

	first, err := service.Create(ctx, userFact("Формат отчётов", "Короткие отчёты с таблицами"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	input := userFact("Формат отчётов", "Короткие отчёты с таблицами")
	input.Source = domainmemory.Source{Kind: domainmemory.SourceDocument, SourceID: 11, Label: "brief.md"}
	second, err := service.Create(ctx, input)
	if err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if !second.Duplicate || second.Entry.ID != first.Entry.ID {
		t.Fatalf("duplicate handling: duplicate=%v ids=%d/%d", second.Duplicate, first.Entry.ID, second.Entry.ID)
	}

	view, err := service.Get(ctx, first.Entry.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(view.Entry.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(view.Entry.Sources))
	}
	kinds := map[domainmemory.SourceKind]bool{}
	for _, source := range view.Entry.Sources {
		kinds[source.Kind] = true
	}
	if !kinds[domainmemory.SourceUser] || !kinds[domainmemory.SourceDocument] {
		t.Fatalf("provenance kinds = %v", kinds)
	}
}

func TestMemorySearchFindsMorphologyAndEntities(t *testing.T) {
	service, cleanup := newMemoryService(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := service.Remember(ctx, appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Проект Atlas", Content: "Работаю над внутренним порталом Atlas на Go",
			Category: domainmemory.CategoryContext, Certainty: domainmemory.CertaintyFact,
			Entities: []string{"Atlas", "Go"},
			Source:   domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Слова пользователя"},
		},
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, err := service.Create(ctx, userFact("Любимый редактор", "Редактор Neovim с плагином telescope")); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, query := range []string{"Atlas", "порталом", "Neovim", "teles"} {
		entries, total, err := service.List(ctx, appmemory.Filter{Query: query})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if total == 0 || len(entries) == 0 {
			t.Fatalf("search %q returned nothing", query)
		}
	}

	// Punctuation and FTS operators in user input must not break the query.
	if _, _, err := service.List(ctx, appmemory.Filter{Query: `Atlas " AND ( -- *`}); err != nil {
		t.Fatalf("hostile query: %v", err)
	}
}

func TestMemoryConflictSurvivesReloadAndResolves(t *testing.T) {
	service, cleanup := newMemoryService(t)
	defer cleanup()
	ctx := context.Background()

	first, err := service.Remember(ctx, appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Часовой пояс", Content: "Живу в Москве",
			Category: domainmemory.CategoryFact, Certainty: domainmemory.CertaintyFact,
			Source: domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Слова пользователя"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	conflicting, err := service.Remember(ctx, appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Часовой пояс", Content: "Живу в Екатеринбурге",
			Category: domainmemory.CategoryFact, Certainty: domainmemory.CertaintyAssumption,
			Source: domainmemory.Source{Kind: domainmemory.SourceConversation, SourceID: 5, Label: "Диалог #5"},
		},
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if conflicting.Conflict == nil {
		t.Fatal("expected a conflict for a contradicting claim")
	}

	views, err := service.Conflicts(ctx, true)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("pending conflicts = %d, want 1", len(views))
	}
	if views[0].Existing.ID != first.Entry.ID || views[0].Candidate.ID != conflicting.Entry.ID {
		t.Fatalf("conflict sides = %d/%d", views[0].Existing.ID, views[0].Candidate.ID)
	}

	if err := service.Resolve(ctx, conflicting.Conflict.ID, appmemory.ResolveInput{Resolution: "candidate"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	old, err := service.Get(ctx, first.Entry.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if old.Entry.Status != domainmemory.StatusSuperseded {
		t.Fatalf("old status = %q, want superseded", old.Entry.Status)
	}
	// The superseded claim stays readable as an auditable version.
	if old.Entry.Content != "Живу в Москве" {
		t.Fatalf("superseded content was rewritten: %q", old.Entry.Content)
	}

	// Superseded entries do not leak into the active memory list.
	active, _, err := service.List(ctx, appmemory.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, entry := range active {
		if entry.ID == first.Entry.ID {
			t.Fatal("superseded entry appeared in the active list")
		}
	}
	if err := service.Resolve(ctx, conflicting.Conflict.ID, appmemory.ResolveInput{Resolution: "existing"}); !errors.Is(err, appmemory.ErrConflictResolved) {
		t.Fatalf("second resolution error = %v, want ErrConflictResolved", err)
	}
}

func TestMemoryVersionConflictIsReported(t *testing.T) {
	service, cleanup := newMemoryService(t)
	defer cleanup()
	ctx := context.Background()

	created, err := service.Create(ctx, userFact("Тема", "Первое значение"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	content := "Обновлённое значение"
	if _, err := service.Update(ctx, created.Entry.ID, created.Entry.Version, appmemory.UpdateInput{Content: &content}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := service.Update(ctx, created.Entry.ID, created.Entry.Version, appmemory.UpdateInput{Content: &content}); !errors.Is(err, appmemory.ErrVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrVersionConflict", err)
	}
	if err := service.Delete(ctx, created.Entry.ID, created.Entry.Version); !errors.Is(err, appmemory.ErrVersionConflict) {
		t.Fatalf("stale delete error = %v, want ErrVersionConflict", err)
	}
}

func TestMemoryLinksAreCheckedAgainstRealObjects(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()
	service := appmemory.NewService(NewMemoryUnitOfWork(db), memoryTestClock{at: time.Now().UTC()})

	entry, err := service.Create(ctx, userFact("Atlas", "Портал Atlas пишется на Go"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Link(ctx, &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetTask, TargetID: 999, Relation: "относится к",
	}); !errors.Is(err, appmemory.ErrLinkTargetNotFound) {
		t.Fatalf("link to a missing task error = %v, want ErrLinkTargetNotFound", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO tasks (title, priority, status, timezone, created_at, updated_at)
		VALUES ('Написать отчёт', 'medium', 'open', 'Europe/Moscow', 0, 0)`); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	link, err := service.Link(ctx, &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetTask, TargetID: 1, Relation: "относится к",
	})
	if err != nil {
		t.Fatalf("link to a real task: %v", err)
	}
	if link.ID == 0 {
		t.Fatal("link was not stored")
	}
	// Goals have no module yet, so a link to one must be refused instead of
	// silently pointing at nothing.
	if _, err := service.Link(ctx, &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetGoal, TargetID: 1, Relation: "относится к",
	}); !errors.Is(err, appmemory.ErrLinkTargetNotFound) {
		t.Fatalf("goal link error = %v, want ErrLinkTargetNotFound", err)
	}
}

type stubExtractor struct {
	candidates []appmemory.Candidate
	docCalls   int
}

func (s *stubExtractor) ExtractConversation(context.Context, string) ([]appmemory.Candidate, error) {
	return s.candidates, nil
}

func (s *stubExtractor) ExtractDocument(context.Context, string, string) ([]appmemory.Candidate, error) {
	s.docCalls++
	return s.candidates, nil
}

func TestImportDocumentCreatesKnowledgeAndExtractedFacts(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	extractor := &stubExtractor{candidates: []appmemory.Candidate{
		{Title: "Регламент релизов", Content: "Релизы выходят по четвергам", Category: domainmemory.CategoryInstruction, Certainty: domainmemory.CertaintyFact, Confidence: 0.9},
		{Title: "Регламент релизов", Content: "Релизы выходят по средам", Category: domainmemory.CategoryInstruction, Certainty: domainmemory.CertaintyAssumption, Confidence: 0.4},
	}}
	service := appmemory.NewService(
		NewMemoryUnitOfWork(db),
		memoryTestClock{at: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		appmemory.WithExtractor(extractor),
	)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO documents (filename, mime, size, sha256, path, status, extracted_text, created_at, updated_at)
		VALUES ('release.md', 'text/markdown', 10, 'abc', '/tmp/release.md', 'processed', '# Релизы

Релизы выходят по четвергам. #процессы', 0, 0)`); err != nil {
		t.Fatalf("insert document: %v", err)
	}

	text := "# Релизы\n\nРелизы выходят по четвергам. #процессы"
	result, err := service.ImportDocument(ctx, &domaindocument.Document{
		ID: 1, Filename: "release.md", Status: domaindocument.StatusProcessed,
		SHA256: "abc", ExtractedText: &text, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Entry == nil || result.Entry.Category != domainmemory.CategoryDocumentation {
		t.Fatalf("document entry = %+v", result.Entry)
	}
	if extractor.docCalls != 1 {
		t.Fatalf("document extraction calls = %d, want 1", extractor.docCalls)
	}
	if result.Extracted != 2 {
		t.Fatalf("extracted facts = %d, want 2", result.Extracted)
	}
	// The second claim contradicts the first, so it is retained for review.
	if result.Conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", result.Conflicts)
	}
	stats, err := service.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Knowledge < 3 {
		t.Fatalf("knowledge entries = %d, want at least 3", stats.Knowledge)
	}
	if stats.PendingConflicts != 1 {
		t.Fatalf("pending conflicts = %d, want 1", stats.PendingConflicts)
	}

	// Re-importing the same document must not duplicate the document entry.
	again, err := service.ImportDocument(ctx, &domaindocument.Document{
		ID: 1, Filename: "release.md", Status: domaindocument.StatusProcessed,
		SHA256: "abc", ExtractedText: &text, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if !again.Duplicate {
		t.Fatal("re-import created a duplicate document entry")
	}
}

func TestMemoryUniqueActiveDedupeGuardsConcurrentLearning(t *testing.T) {
	service, db, cleanup := newMemoryServiceWithDB(t)
	defer cleanup()
	ctx := context.Background()

	created, err := service.Create(ctx, userFact("Часовой пояс", "Живу в Москве"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, err := service.Get(ctx, created.Entry.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// A second writer that lost the race at the service level would reach the
	// table with the same dedupe key; the database must reject it instead of
	// letting two active copies of one claim exist.
	duplicate := *stored.Entry
	duplicate.ID = 0
	duplicate.Sources = []domainmemory.Source{stored.Entry.Sources[0]}
	duplicate.Sources[0].ID = 0
	err = NewMemoryUnitOfWork(db).Do(ctx, func(ctx context.Context, tx appmemory.Transaction) error {
		_, err := tx.Memory().InsertEntry(ctx, &duplicate)
		return err
	})
	if err == nil {
		t.Fatal("the second active copy of the same claim was accepted")
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate rejected for the wrong reason: %v", err)
	}
}

func TestDocumentReimportCreatesVersionAndSupersedes(t *testing.T) {
	service, cleanup := newMemoryService(t)
	defer cleanup()
	ctx := context.Background()

	first := "# Регламент\n\nРелизы по четвергам."
	firstResult, err := service.ImportDocument(ctx, &domaindocument.Document{
		ID: 1, Filename: "release.md", Status: domaindocument.StatusProcessed,
		SHA256: "hash-v1", ExtractedText: &first, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second := "# Регламент\n\nРелизы по средам."
	secondResult, err := service.ImportDocument(ctx, &domaindocument.Document{
		ID: 2, Filename: "release.md", Status: domaindocument.StatusProcessed,
		SHA256: "hash-v2", ExtractedText: &second, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if secondResult.Entry.ID == firstResult.Entry.ID {
		t.Fatal("a new document version must create a new entry")
	}

	previous, err := service.Get(ctx, firstResult.Entry.ID)
	if err != nil {
		t.Fatalf("get previous: %v", err)
	}
	if previous.Entry.Status != domainmemory.StatusSuperseded {
		t.Fatalf("previous document version status = %q, want superseded", previous.Entry.Status)
	}
	if previous.Entry.SupersededBy == nil || *previous.Entry.SupersededBy != secondResult.Entry.ID {
		t.Fatal("superseded document version must point at its replacement")
	}
	current, err := service.Get(ctx, secondResult.Entry.ID)
	if err != nil {
		t.Fatalf("get current: %v", err)
	}
	if current.Entry.Status != domainmemory.StatusActive {
		t.Fatalf("current document version status = %q, want active", current.Entry.Status)
	}
	if current.Entry.DocumentSHA != "hash-v2" {
		t.Fatalf("document hash = %q, want hash-v2", current.Entry.DocumentSHA)
	}
}
