package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func testClock() fixedClock {
	return fixedClock{at: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
}

// fakeRepo is an in-memory Repository that mirrors the SQLite semantics the
// service depends on: dedupe by key, same-subject lookup, versioned writes.
type fakeRepo struct {
	entries   map[int64]*domainmemory.Entry
	sources   map[int64][]domainmemory.Source
	links     []domainmemory.Link
	conflicts map[int64]*domainmemory.Conflict
	nextID    int64
	nextLink  int64
	nextConf  int64
	// targets lists object ids that Link may point at.
	targets map[domainmemory.TargetType]map[int64]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		entries:   make(map[int64]*domainmemory.Entry),
		sources:   make(map[int64][]domainmemory.Source),
		conflicts: make(map[int64]*domainmemory.Conflict),
		targets:   map[domainmemory.TargetType]map[int64]bool{domainmemory.TargetMemory: {}},
	}
}

func (r *fakeRepo) clone(entry *domainmemory.Entry) *domainmemory.Entry {
	copyEntry := *entry
	copyEntry.Entities = append([]string(nil), entry.Entities...)
	copyEntry.Sources = append([]domainmemory.Source(nil), r.sources[entry.ID]...)
	return &copyEntry
}

func (r *fakeRepo) InsertEntry(_ context.Context, entry *domainmemory.Entry) (int64, error) {
	if err := entry.Validate(); err != nil {
		return 0, err
	}
	r.nextID++
	entry.ID = r.nextID
	stored := *entry
	stored.Sources = nil
	r.entries[entry.ID] = &stored
	r.sources[entry.ID] = append([]domainmemory.Source(nil), entry.Sources...)
	return entry.ID, nil
}

func (r *fakeRepo) GetEntry(_ context.Context, id int64) (*domainmemory.Entry, error) {
	entry, ok := r.entries[id]
	if !ok {
		return nil, appmemory.ErrNotFound
	}
	return r.clone(entry), nil
}

func (r *fakeRepo) UpdateEntry(_ context.Context, id int64, expectedVersion int, entry *domainmemory.Entry) (*domainmemory.Entry, error) {
	stored, ok := r.entries[id]
	if !ok {
		return nil, appmemory.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appmemory.ErrVersionConflict
	}
	updated := *entry
	updated.ID = id
	updated.Version = expectedVersion + 1
	r.entries[id] = &updated
	return r.GetEntry(context.Background(), id)
}

func (r *fakeRepo) SetStatus(_ context.Context, id int64, expectedVersion int, status domainmemory.Status, supersededBy *int64, at time.Time) (*domainmemory.Entry, error) {
	stored, ok := r.entries[id]
	if !ok {
		return nil, appmemory.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appmemory.ErrVersionConflict
	}
	stored.Status = status
	stored.SupersededBy = supersededBy
	stored.UpdatedAt = at
	stored.Version++
	return r.GetEntry(context.Background(), id)
}

func (r *fakeRepo) DeleteEntry(_ context.Context, id int64, expectedVersion int) error {
	stored, ok := r.entries[id]
	if !ok {
		return appmemory.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return appmemory.ErrVersionConflict
	}
	delete(r.entries, id)
	return nil
}

func (r *fakeRepo) ListEntries(_ context.Context, filter appmemory.Filter) ([]*domainmemory.Entry, int, error) {
	matches := make([]*domainmemory.Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		if filter.Area != nil && entry.Area() != *filter.Area {
			continue
		}
		if filter.Category != nil && entry.Category != *filter.Category {
			continue
		}
		if filter.Status != nil {
			if entry.Status != *filter.Status {
				continue
			}
		} else if entry.Status == domainmemory.StatusSuperseded {
			continue
		}
		matches = append(matches, r.clone(entry))
	}
	return matches, len(matches), nil
}

func (r *fakeRepo) FindSameSubject(_ context.Context, area domainmemory.Area, category domainmemory.Category, normalizedTitle string) ([]*domainmemory.Entry, error) {
	result := make([]*domainmemory.Entry, 0)
	for _, entry := range r.entries {
		if entry.Area() != area || entry.Category != category || entry.NormalizedTitle != normalizedTitle {
			continue
		}
		if entry.Status == domainmemory.StatusSuperseded {
			continue
		}
		result = append(result, r.clone(entry))
	}
	return result, nil
}

func (r *fakeRepo) FindByDedupeKey(_ context.Context, key string) (*domainmemory.Entry, error) {
	for _, entry := range r.entries {
		if entry.DedupeKey == key && entry.Status != domainmemory.StatusSuperseded {
			return r.clone(entry), nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) AddSource(_ context.Context, entryID int64, source domainmemory.Source) error {
	r.sources[entryID] = append(r.sources[entryID], source)
	return nil
}

func (r *fakeRepo) Stats(context.Context) (appmemory.Stats, error) {
	var stats appmemory.Stats
	for _, entry := range r.entries {
		if entry.Status == domainmemory.StatusSuperseded {
			continue
		}
		if entry.Area() == domainmemory.AreaPersonal {
			stats.Personal++
		} else {
			stats.Knowledge++
		}
	}
	for _, conflict := range r.conflicts {
		if conflict.Status == domainmemory.ConflictPending {
			stats.PendingConflicts++
		}
	}
	return stats, nil
}

func (r *fakeRepo) InsertLink(_ context.Context, link *domainmemory.Link) (int64, error) {
	if err := link.Validate(); err != nil {
		return 0, err
	}
	for _, existing := range r.links {
		if existing.EntryID == link.EntryID && existing.TargetType == link.TargetType && existing.TargetID == link.TargetID && existing.Relation == link.Relation {
			return existing.ID, nil
		}
	}
	r.nextLink++
	link.ID = r.nextLink
	r.links = append(r.links, *link)
	return link.ID, nil
}

func (r *fakeRepo) ListLinks(_ context.Context, entryID int64) ([]domainmemory.Link, error) {
	result := make([]domainmemory.Link, 0)
	for _, link := range r.links {
		if link.EntryID == entryID {
			result = append(result, link)
		}
	}
	return result, nil
}

// TargetExists mirrors the SQLite implementation: the check happens inside the
// transaction that inserts the link.
func (r *fakeRepo) TargetExists(_ context.Context, target domainmemory.TargetType, id int64) (bool, error) {
	if target == domainmemory.TargetMemory {
		_, ok := r.entries[id]
		return ok, nil
	}
	known, ok := r.targets[target]
	if !ok {
		return false, nil
	}
	return known[id], nil
}

func (r *fakeRepo) DeleteLink(_ context.Context, id, entryID int64) error {
	for index, link := range r.links {
		if link.ID == id && link.EntryID == entryID {
			r.links = append(r.links[:index], r.links[index+1:]...)
			return nil
		}
	}
	return appmemory.ErrLinkNotFound
}

func (r *fakeRepo) InsertConflict(_ context.Context, conflict *domainmemory.Conflict) (int64, error) {
	r.nextConf++
	conflict.ID = r.nextConf
	stored := *conflict
	r.conflicts[conflict.ID] = &stored
	return conflict.ID, nil
}

func (r *fakeRepo) GetConflict(_ context.Context, id int64) (*domainmemory.Conflict, error) {
	conflict, ok := r.conflicts[id]
	if !ok {
		return nil, appmemory.ErrConflictNotFound
	}
	copyConflict := *conflict
	return &copyConflict, nil
}

func (r *fakeRepo) ListConflicts(_ context.Context, pendingOnly bool) ([]*domainmemory.Conflict, error) {
	result := make([]*domainmemory.Conflict, 0)
	for _, conflict := range r.conflicts {
		if pendingOnly && conflict.Status != domainmemory.ConflictPending {
			continue
		}
		copyConflict := *conflict
		result = append(result, &copyConflict)
	}
	return result, nil
}

func (r *fakeRepo) ResolveConflict(_ context.Context, id int64, resolution string, at time.Time) error {
	conflict, ok := r.conflicts[id]
	if !ok {
		return appmemory.ErrConflictNotFound
	}
	if conflict.Status == domainmemory.ConflictResolved {
		return appmemory.ErrConflictResolved
	}
	conflict.Status = domainmemory.ConflictResolved
	conflict.Resolution = resolution
	conflict.ResolvedAt = &at
	return nil
}

type fakeTx struct{ repo *fakeRepo }

func (t fakeTx) Memory() appmemory.Repository { return t.repo }

type fakeUOW struct{ repo *fakeRepo }

func (u fakeUOW) Do(ctx context.Context, fn func(context.Context, appmemory.Transaction) error) error {
	return fn(ctx, fakeTx{repo: u.repo})
}

func newService(repo *fakeRepo) *appmemory.Service {
	return appmemory.NewService(fakeUOW{repo: repo}, testClock())
}

func personalInput(title, content string) appmemory.CreateInput {
	confidence := 1.0
	return appmemory.CreateInput{
		Title: title, Content: content,
		Category: domainmemory.CategoryPreference, Certainty: domainmemory.CertaintyFact,
		Confidence: &confidence,
		Source:     domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Слова пользователя"},
	}
}

func TestCreateKeepsProvenanceAndVersion(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)

	result, err := service.Create(context.Background(), personalInput("Формат отчётов", "Люблю короткие отчёты с таблицами"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result.Entry.Area() != domainmemory.AreaPersonal {
		t.Fatalf("area = %q, want personal", result.Entry.Area())
	}
	if len(result.Entry.Sources) != 1 || result.Entry.Sources[0].Kind != domainmemory.SourceUser {
		t.Fatalf("provenance was not stored: %#v", result.Entry.Sources)
	}
	if result.Entry.Version != 1 {
		t.Fatalf("version = %d, want 1", result.Entry.Version)
	}
	if result.Entry.Confidence != 1 {
		t.Fatalf("confidence = %v, want 1", result.Entry.Confidence)
	}
}

func TestDuplicateAddsSourceInsteadOfEntry(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	input := personalInput("Часовой пояс", "Живу в Москве")

	first, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	input.Source = domainmemory.Source{Kind: domainmemory.SourceDocument, SourceID: 7, Label: "Анкета"}
	second, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("identical fact should be reported as a duplicate")
	}
	if second.Entry.ID != first.Entry.ID {
		t.Fatalf("duplicate created a new entry %d instead of %d", second.Entry.ID, first.Entry.ID)
	}
	if len(second.Entry.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 provenance records", len(second.Entry.Sources))
	}
}

func TestConflictingClaimNeedsUserDecision(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	if _, err := service.Create(context.Background(), personalInput("Рабочие часы", "Начинаю работу в 9:00")); err != nil {
		t.Fatalf("create: %v", err)
	}

	// An assumption never replaces an established fact on its own.
	confidence := 0.5
	conflicting, err := service.Remember(context.Background(), appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Рабочие часы", Content: "Начинаю работу в 11:00",
			Category: domainmemory.CategoryPreference, Certainty: domainmemory.CertaintyAssumption,
			Confidence: &confidence,
			Source:     domainmemory.Source{Kind: domainmemory.SourceConversation, SourceID: 3, Label: "Диалог #3"},
		},
		ReplaceSameSubject: true,
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if conflicting.Conflict == nil {
		t.Fatal("a contradicting assumption must create a reviewable conflict")
	}
	if conflicting.Entry.Status != domainmemory.StatusDisputed {
		t.Fatalf("candidate status = %q, want disputed", conflicting.Entry.Status)
	}
	views, err := service.Conflicts(context.Background(), true)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("pending conflicts = %d, want 1", len(views))
	}
	if views[0].Existing == nil || views[0].Candidate == nil {
		t.Fatal("both claims must be visible for review")
	}
}

func TestResolveCandidateSupersedesOldFact(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	first, err := service.Create(context.Background(), personalInput("Рабочие часы", "Начинаю работу в 9:00"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	confidence := 0.4
	second, err := service.Remember(context.Background(), appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Рабочие часы", Content: "Начинаю работу в 11:00",
			Category: domainmemory.CategoryPreference, Certainty: domainmemory.CertaintyInference,
			Confidence: &confidence,
			Source:     domainmemory.Source{Kind: domainmemory.SourceConversation, SourceID: 4, Label: "Диалог #4"},
		},
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if second.Conflict == nil {
		t.Fatal("expected a conflict")
	}
	if err := service.Resolve(context.Background(), second.Conflict.ID, appmemory.ResolveInput{Resolution: "candidate"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	old, err := service.Get(context.Background(), first.Entry.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if old.Entry.Status != domainmemory.StatusSuperseded {
		t.Fatalf("old status = %q, want superseded", old.Entry.Status)
	}
	if old.Entry.SupersededBy == nil || *old.Entry.SupersededBy != second.Entry.ID {
		t.Fatal("superseded entry must point at its replacement")
	}
	active, err := service.Get(context.Background(), second.Entry.ID)
	if err != nil {
		t.Fatalf("get new: %v", err)
	}
	if active.Entry.Status != domainmemory.StatusActive {
		t.Fatalf("chosen entry status = %q, want active", active.Entry.Status)
	}
}

func TestResolveTwiceIsRejected(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	first, _ := service.Create(context.Background(), personalInput("Тема", "Первое"))
	second, err := service.Remember(context.Background(), appmemory.RememberInput{
		CreateInput: appmemory.CreateInput{
			Title: "Тема", Content: "Второе", Category: domainmemory.CategoryPreference,
			Certainty: domainmemory.CertaintyAssumption,
			Source:    domainmemory.Source{Kind: domainmemory.SourceConversation, Label: "Диалог"},
		},
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := service.Resolve(context.Background(), second.Conflict.ID, appmemory.ResolveInput{Resolution: "existing"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	err = service.Resolve(context.Background(), second.Conflict.ID, appmemory.ResolveInput{Resolution: "candidate"})
	if !errors.Is(err, appmemory.ErrConflictResolved) {
		t.Fatalf("second resolution error = %v, want ErrConflictResolved", err)
	}
	_ = first
}

func TestUpdateRequiresMatchingVersion(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), personalInput("Город", "Москва"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	title := "Город проживания"
	updated, err := service.Update(context.Background(), created.Entry.ID, created.Entry.Version, appmemory.UpdateInput{Title: &title})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != title || updated.Version != 2 {
		t.Fatalf("update result = %+v", updated)
	}
	if _, err := service.Update(context.Background(), created.Entry.ID, 1, appmemory.UpdateInput{Title: &title}); !errors.Is(err, appmemory.ErrVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrVersionConflict", err)
	}
}

func TestValidationRejectsEmptyAndOversizedContent(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	if _, err := service.Create(context.Background(), personalInput("", "текст")); !errors.Is(err, domainmemory.ErrInvalidTitle) {
		t.Fatalf("empty title error = %v", err)
	}
	if _, err := service.Create(context.Background(), personalInput("Тема", "")); !errors.Is(err, domainmemory.ErrInvalidContent) {
		t.Fatalf("empty content error = %v", err)
	}
	invalid := personalInput("Тема", "текст")
	invalid.Category = "unknown"
	if _, err := service.Create(context.Background(), invalid); !errors.Is(err, domainmemory.ErrInvalidCategory) {
		t.Fatalf("invalid category error = %v", err)
	}
}

func TestContextSnapshotSeparatesCertainty(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	if _, err := service.Create(context.Background(), personalInput("Формат", "Короткие отчёты")); err != nil {
		t.Fatalf("create: %v", err)
	}
	snapshot, err := service.ContextSnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, want := range []string{"Память:", "Формат", "fact", "search_memory"} {
		if !contains(snapshot, want) {
			t.Fatalf("snapshot missing %q: %s", want, snapshot)
		}
	}
}

func TestLinkRejectsUnknownTargetWithoutResolver(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	entry, _ := service.Create(context.Background(), personalInput("Проект", "Проект LifeOS"))
	repo.targets[domainmemory.TargetTask] = map[int64]bool{42: true}
	link, err := service.Link(context.Background(), &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetTask, TargetID: 42, Relation: "относится к",
	})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if link.ID == 0 {
		t.Fatal("link was not stored")
	}
	duplicate, err := service.Link(context.Background(), &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetTask, TargetID: 42, Relation: "относится к",
	})
	if err != nil {
		t.Fatalf("duplicate link: %v", err)
	}
	if duplicate.ID != link.ID {
		t.Fatal("the same relationship must not be stored twice")
	}
	if err := service.Unlink(context.Background(), link.ID, entry.Entry.ID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if err := service.Unlink(context.Background(), link.ID, entry.Entry.ID); !errors.Is(err, appmemory.ErrLinkNotFound) {
		t.Fatalf("second unlink error = %v, want ErrLinkNotFound", err)
	}
}

func TestLinkRejectsUnknownTarget(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	entry, _ := service.Create(context.Background(), personalInput("Проект", "Проект LifeOS"))
	if _, err := service.Link(context.Background(), &domainmemory.Link{
		EntryID: entry.Entry.ID, TargetType: domainmemory.TargetTask, TargetID: 7, Relation: "относится к",
	}); !errors.Is(err, appmemory.ErrLinkTargetNotFound) {
		t.Fatalf("link error = %v, want ErrLinkTargetNotFound", err)
	}
	if len(repo.links) != 0 {
		t.Fatal("a rejected link must not be stored")
	}
}

func contains(value, substring string) bool {
	return len(value) >= len(substring) && (func() bool {
		for i := 0; i+len(substring) <= len(value); i++ {
			if value[i:i+len(substring)] == substring {
				return true
			}
		}
		return false
	})()
}
