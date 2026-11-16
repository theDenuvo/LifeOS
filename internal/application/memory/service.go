package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	domaindocument "github.com/theDenuvo/LifeOS/internal/domain/document"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
	"github.com/theDenuvo/LifeOS/internal/events"
)

const (
	defaultLimit = 30
	maxLimit     = 100
)

// CreateInput is a user- or model-created durable statement.
type CreateInput struct {
	Title       string
	Content     string
	Category    domainmemory.Category
	Certainty   domainmemory.Certainty
	Confidence  *float64
	Entities    []string
	Source      domainmemory.Source
	DocumentSHA string
}

// RememberInput controls deduplication and safe automatic supersession.
type RememberInput struct {
	CreateInput
	SupersedesID       int64
	ReplaceSameSubject bool
}

// RememberResult explains whether a new entry, a source merge, or a conflict
// was produced. The caller never has to guess what happened.
type RememberResult struct {
	Entry     *domainmemory.Entry
	Duplicate bool
	Conflict  *domainmemory.Conflict
}

// UpdateInput is a partial, optimistic-locked user edit.
type UpdateInput struct {
	Title      *string
	Content    *string
	Category   *domainmemory.Category
	Certainty  *domainmemory.Certainty
	Confidence *float64
	Entities   *[]string
}

// View is an entry with all of its outgoing relationships.
type View struct {
	Entry *domainmemory.Entry
	Links []domainmemory.Link
}

// ResolveInput is a reviewed conflict resolution. merged_title/content are
// required only for the "merge" resolution.
type ResolveInput struct {
	Resolution    string
	MergedTitle   string
	MergedContent string
}

// Service coordinates memory mutations, learning, provenance and conflicts.
type Service struct {
	uow       UnitOfWork
	clock     Clock
	extractor Extractor
	publisher events.Publisher
}

// Option customises the service.
type Option func(*Service)

func WithExtractor(extractor Extractor) Option { return func(s *Service) { s.extractor = extractor } }
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) { s.publisher = publisher }
}

func NewService(uow UnitOfWork, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	s := &Service{uow: uow, clock: clock}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Create stores a manually reviewed entry. Conflicting subjects are retained
// as a review item instead of silently overwriting memory.
func (s *Service) Create(ctx context.Context, input CreateInput) (RememberResult, error) {
	return s.Remember(ctx, RememberInput{CreateInput: input})
}

// Remember learns one statement. Exact duplicates only gain provenance;
// authoritative corrections supersede the old version; ambiguous alternatives
// are marked disputed and create a user-reviewable conflict.
func (s *Service) Remember(ctx context.Context, input RememberInput) (result RememberResult, err error) {
	now := s.clock.Now().UTC()
	entry, err := buildEntry(input.CreateInput, now)
	if err != nil {
		return RememberResult{}, err
	}

	var eventType string
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Memory()
		duplicate, findErr := repo.FindByDedupeKey(ctx, entry.DedupeKey)
		if findErr != nil {
			return findErr
		}
		if duplicate != nil {
			if !hasSource(duplicate.Sources, entry.Sources[0]) {
				if err := repo.AddSource(ctx, duplicate.ID, entry.Sources[0]); err != nil {
					return err
				}
			}
			duplicate, err = repo.GetEntry(ctx, duplicate.ID)
			if err != nil {
				return err
			}
			result.Entry = duplicate
			result.Duplicate = true
			return nil
		}

		sameSubject, err := repo.FindSameSubject(ctx, entry.Area(), entry.Category, entry.NormalizedTitle)
		if err != nil {
			return err
		}
		if len(sameSubject) == 0 {
			id, err := repo.InsertEntry(ctx, entry)
			if err != nil {
				return err
			}
			entry.ID = id
			result.Entry, err = repo.GetEntry(ctx, id)
			eventType = "memory.created"
			return err
		}

		var previous *domainmemory.Entry
		if input.SupersedesID != 0 {
			for _, candidate := range sameSubject {
				if candidate.ID == input.SupersedesID {
					previous = candidate
					break
				}
			}
			if previous == nil {
				return fmt.Errorf("%w: supersedes_id must identify the same subject", domainmemory.ErrInvalidEntry)
			}
		} else if input.ReplaceSameSubject && canAutomaticallyReplace(entry) {
			previous = sameSubject[0]
		}
		if previous != nil {
			id, err := repo.InsertEntry(ctx, entry)
			if err != nil {
				return err
			}
			entry.ID = id
			if _, err := repo.SetStatus(ctx, previous.ID, previous.Version, domainmemory.StatusSuperseded, &id, now); err != nil {
				return err
			}
			result.Entry, err = repo.GetEntry(ctx, id)
			eventType = "memory.updated"
			return err
		}

		entry.Status = domainmemory.StatusDisputed
		id, err := repo.InsertEntry(ctx, entry)
		if err != nil {
			return err
		}
		entry.ID = id
		conflict := &domainmemory.Conflict{
			ExistingID: sameSubject[0].ID, CandidateID: id,
			Reason: "Одинаковая тема содержит разные сведения. Нужно выбрать актуальную версию или оставить обе.",
			Status: domainmemory.ConflictPending, CreatedAt: now,
		}
		conflictID, err := repo.InsertConflict(ctx, conflict)
		if err != nil {
			return err
		}
		conflict.ID = conflictID
		result.Entry, err = repo.GetEntry(ctx, id)
		if err != nil {
			return err
		}
		result.Conflict = conflict
		eventType = "memory.conflict.detected"
		return nil
	})
	if err != nil {
		return RememberResult{}, err
	}
	if result.Duplicate {
		eventType = "memory.source.added"
	}
	s.publish(eventType, map[string]any{"id": result.Entry.ID, "version": result.Entry.Version})
	return result, nil
}

// List returns active entries by default and performs Unicode-aware full-text
// search in SQLite when a query is present.
func (s *Service) List(ctx context.Context, filter Filter) ([]*domainmemory.Entry, int, error) {
	filter.Limit, filter.Offset = page(filter.Limit, filter.Offset)
	var entries []*domainmemory.Entry
	var total int
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		entries, total, err = tx.Memory().ListEntries(ctx, filter)
		return err
	})
	return entries, total, err
}

func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	var view View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		entry, err := tx.Memory().GetEntry(ctx, id)
		if err != nil {
			return err
		}
		links, err := tx.Memory().ListLinks(ctx, id)
		if err != nil {
			return err
		}
		view = View{Entry: entry, Links: links}
		return nil
	})
	return view, err
}

// Update applies a user edit under optimistic locking and recalculates the
// normalized deduplication keys.
func (s *Service) Update(ctx context.Context, id int64, version int, input UpdateInput) (*domainmemory.Entry, error) {
	now := s.clock.Now().UTC()
	var updated *domainmemory.Entry
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		entry, err := tx.Memory().GetEntry(ctx, id)
		if err != nil {
			return err
		}
		if input.Title != nil {
			entry.Title = strings.TrimSpace(*input.Title)
		}
		if input.Content != nil {
			entry.Content = strings.TrimSpace(*input.Content)
		}
		if input.Category != nil {
			entry.Category = *input.Category
		}
		if input.Certainty != nil {
			entry.Certainty = *input.Certainty
		}
		if input.Confidence != nil {
			entry.Confidence = *input.Confidence
		}
		if input.Entities != nil {
			entry.Entities = normalizeEntities(*input.Entities)
		}
		entry.NormalizedTitle = normalizeTitle(entry.Title)
		entry.DedupeKey = dedupeKey(entry)
		entry.UpdatedAt = now
		if err := entry.Validate(); err != nil {
			return err
		}
		updated, err = tx.Memory().UpdateEntry(ctx, id, version, entry)
		return err
	})
	if err == nil {
		s.publish("memory.updated", map[string]any{"id": id, "version": updated.Version})
	}
	return updated, err
}

func (s *Service) Delete(ctx context.Context, id int64, version int) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Memory().DeleteEntry(ctx, id, version)
	})
	if err == nil {
		s.publish("memory.deleted", map[string]any{"id": id})
	}
	return err
}

// Link relates an entry to another LifeOS object while preserving a named,
// user-editable relationship.
func (s *Service) Link(ctx context.Context, link *domainmemory.Link) (*domainmemory.Link, error) {
	link.Relation = strings.TrimSpace(link.Relation)
	link.CreatedAt = s.clock.Now().UTC()
	if err := link.Validate(); err != nil {
		return nil, err
	}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		if _, err := tx.Memory().GetEntry(ctx, link.EntryID); err != nil {
			return err
		}
		exists, err := tx.Memory().TargetExists(ctx, link.TargetType, link.TargetID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrLinkTargetNotFound
		}
		id, err := tx.Memory().InsertLink(ctx, link)
		if err != nil {
			return err
		}
		link.ID = id
		return nil
	})
	if err == nil {
		s.publish("memory.linked", map[string]any{"entry_id": link.EntryID, "link_id": link.ID})
	}
	return link, err
}

func (s *Service) Unlink(ctx context.Context, id, entryID int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Memory().DeleteLink(ctx, id, entryID)
	})
	if err == nil {
		s.publish("memory.unlinked", map[string]any{"entry_id": entryID, "link_id": id})
	}
	return err
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		stats, err = tx.Memory().Stats(ctx)
		return err
	})
	return stats, err
}

func (s *Service) Conflicts(ctx context.Context, pendingOnly bool) ([]ConflictView, error) {
	views := make([]ConflictView, 0)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		conflicts, err := tx.Memory().ListConflicts(ctx, pendingOnly)
		if err != nil {
			return err
		}
		for _, conflict := range conflicts {
			existing, err := tx.Memory().GetEntry(ctx, conflict.ExistingID)
			if err != nil {
				return err
			}
			candidate, err := tx.Memory().GetEntry(ctx, conflict.CandidateID)
			if err != nil {
				return err
			}
			views = append(views, ConflictView{Conflict: conflict, Existing: existing, Candidate: candidate})
		}
		return nil
	})
	return views, err
}

// Resolve applies the user's choice atomically and leaves the losing claims in
// the version history rather than erasing the evidence.
func (s *Service) Resolve(ctx context.Context, id int64, input ResolveInput) error {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Memory()
		conflict, err := repo.GetConflict(ctx, id)
		if err != nil {
			return err
		}
		if conflict.Status == domainmemory.ConflictResolved {
			return ErrConflictResolved
		}
		existing, err := repo.GetEntry(ctx, conflict.ExistingID)
		if err != nil {
			return err
		}
		candidate, err := repo.GetEntry(ctx, conflict.CandidateID)
		if err != nil {
			return err
		}

		switch input.Resolution {
		case "existing":
			if _, err := repo.SetStatus(ctx, candidate.ID, candidate.Version, domainmemory.StatusSuperseded, &existing.ID, now); err != nil {
				return err
			}
		case "candidate":
			if _, err := repo.SetStatus(ctx, existing.ID, existing.Version, domainmemory.StatusSuperseded, &candidate.ID, now); err != nil {
				return err
			}
			if _, err := repo.SetStatus(ctx, candidate.ID, candidate.Version, domainmemory.StatusActive, nil, now); err != nil {
				return err
			}
		case "both":
			if _, err := repo.SetStatus(ctx, existing.ID, existing.Version, domainmemory.StatusActive, nil, now); err != nil {
				return err
			}
			if _, err := repo.SetStatus(ctx, candidate.ID, candidate.Version, domainmemory.StatusActive, nil, now); err != nil {
				return err
			}
		case "merge":
			existing.Title = strings.TrimSpace(input.MergedTitle)
			existing.Content = strings.TrimSpace(input.MergedContent)
			existing.NormalizedTitle = normalizeTitle(existing.Title)
			existing.DedupeKey = dedupeKey(existing)
			existing.UpdatedAt = now
			existing.Sources = appendNewSource(existing.Sources, domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Объединение противоречия", ObservedAt: now})
			if err := existing.Validate(); err != nil {
				return err
			}
			if _, err := repo.UpdateEntry(ctx, existing.ID, existing.Version, existing); err != nil {
				return err
			}
			if _, err := repo.SetStatus(ctx, candidate.ID, candidate.Version, domainmemory.StatusSuperseded, &existing.ID, now); err != nil {
				return err
			}
		default:
			return ErrInvalidResolution
		}
		return repo.ResolveConflict(ctx, id, input.Resolution, now)
	})
	if err == nil {
		s.publish("memory.conflict.resolved", map[string]any{"id": id, "resolution": input.Resolution})
	}
	return err
}

// LearnConversation extracts only durable information. It is intentionally
// separate from task capture: transient plans and one-off actions stay in their
// own modules.
func (s *Service) LearnConversation(ctx context.Context, conversationID, messageID int64, text string) (int, error) {
	if s.extractor == nil || strings.TrimSpace(text) == "" {
		return 0, nil
	}
	candidates, err := s.extractor.ExtractConversation(ctx, truncateRunes(text, 8000))
	if err != nil {
		return 0, err
	}
	learned := 0
	var errs []error
	for _, candidate := range candidates {
		evidence := candidate.Evidence
		if strings.TrimSpace(evidence) == "" {
			evidence = truncateRunes(text, 2000)
		}
		remembered, err := s.Remember(ctx, RememberInput{
			CreateInput: candidateInput(candidate, domainmemory.Source{
				Kind: domainmemory.SourceConversation, SourceID: conversationID,
				Label:   fmt.Sprintf("Диалог #%d · сообщение #%d", conversationID, messageID),
				Excerpt: truncateRunes(evidence, 2000), ObservedAt: s.clock.Now().UTC(),
			}), ReplaceSameSubject: candidate.Correction,
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		learned++
		// Every learned claim stays traceable to the conversation it came from.
		_, _ = s.Link(ctx, &domainmemory.Link{
			EntryID: remembered.Entry.ID, TargetType: domainmemory.TargetConversation,
			TargetID: conversationID, Relation: "learned_from",
		})
	}
	return learned, errors.Join(errs...)
}

// ImportDocument indexes the whole document as reusable knowledge and then
// extracts smaller facts/entities. Same-name processed documents form an
// explicit version chain; exact re-imports merge provenance instead.
func (s *Service) ImportDocument(ctx context.Context, value *domaindocument.Document) (ImportResult, error) {
	var result ImportResult
	if value == nil || value.Status != domaindocument.StatusProcessed || value.ExtractedText == nil {
		return result, fmt.Errorf("%w: document is not processed", domainmemory.ErrInvalidContent)
	}
	text := strings.TrimSpace(*value.ExtractedText)
	if text == "" {
		return result, domainmemory.ErrInvalidContent
	}
	base, err := s.Remember(ctx, RememberInput{
		CreateInput: CreateInput{
			Title: value.Filename, Content: text, Category: domainmemory.CategoryDocumentation,
			Certainty: domainmemory.CertaintyFact, Confidence: float64Ptr(1),
			Entities: documentEntities(text), DocumentSHA: value.SHA256,
			Source: domainmemory.Source{Kind: domainmemory.SourceDocument, SourceID: value.ID, Label: value.Filename, Excerpt: truncateRunes(text, 1000), ObservedAt: value.UpdatedAt},
		}, ReplaceSameSubject: true,
	})
	if err != nil {
		return result, err
	}
	result.Entry = base.Entry
	result.Duplicate = base.Duplicate
	if base.Conflict != nil {
		result.Conflicts++
	}
	if s.extractor == nil {
		return result, nil
	}

	candidates, extractionErr := s.extractor.ExtractDocument(ctx, value.Filename, truncateRunes(text, 32000))
	if extractionErr != nil {
		result.ExtractionError = extractionErr.Error()
		return result, nil
	}
	for _, candidate := range candidates {
		if candidate.Category == domainmemory.CategoryDocumentation && normalizeTitle(candidate.Title) == normalizeTitle(value.Filename) {
			continue
		}
		evidence := candidate.Evidence
		if evidence == "" {
			evidence = truncateRunes(candidate.Content, 1000)
		}
		remembered, err := s.Remember(ctx, RememberInput{
			CreateInput: candidateInput(candidate, domainmemory.Source{
				Kind: domainmemory.SourceDocument, SourceID: value.ID, Label: value.Filename,
				Excerpt: truncateRunes(evidence, 2000), ObservedAt: value.UpdatedAt,
			}), ReplaceSameSubject: candidate.Correction,
		})
		if err != nil {
			continue
		}
		result.Extracted++
		if remembered.Conflict != nil {
			result.Conflicts++
		}
		_, _ = s.Link(ctx, &domainmemory.Link{EntryID: remembered.Entry.ID, TargetType: domainmemory.TargetDocument, TargetID: value.ID, Relation: "derived_from"})
	}
	_, _ = s.Link(ctx, &domainmemory.Link{EntryID: base.Entry.ID, TargetType: domainmemory.TargetDocument, TargetID: value.ID, Relation: "source"})
	return result, nil
}

// ContextSnapshot injects active personal memory, not unresolved claims, into
// every assistant turn. Sources and uncertainty stay visible to the model.
func (s *Service) ContextSnapshot(ctx context.Context) (string, error) {
	area := domainmemory.AreaPersonal
	entries, _, err := s.List(ctx, Filter{Area: &area, Limit: 12})
	if err != nil {
		return "", err
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Память: %d личных сведений, %d материалов знаний, противоречий на проверку %d.\n", stats.Personal, stats.Knowledge, stats.PendingConflicts)
	for _, entry := range entries {
		fmt.Fprintf(&b, "- #%d [%s; %s; %.2f] %s: %s", entry.ID, entry.Category, entry.Certainty, entry.Confidence, entry.Title, truncateRunes(entry.Content, 320))
		if len(entry.Sources) > 0 {
			fmt.Fprintf(&b, " (источник: %s)", entry.Sources[0].Label)
		}
		b.WriteString("\n")
	}
	b.WriteString("Для поиска по всем знаниям используй search_memory; предположения и выводы не выдавай за факты.\n")
	return b.String(), nil
}

func buildEntry(input CreateInput, now time.Time) (*domainmemory.Entry, error) {
	if input.Certainty == "" {
		input.Certainty = domainmemory.CertaintyFact
	}
	if input.Confidence == nil {
		confidence := 1.0
		if input.Source.Kind != domainmemory.SourceUser {
			confidence = 0.75
		}
		input.Confidence = &confidence
	}
	if !input.Source.Kind.IsValid() {
		input.Source.Kind = domainmemory.SourceUser
	}
	if input.Source.ObservedAt.IsZero() {
		input.Source.ObservedAt = now
	}
	if strings.TrimSpace(input.Source.Label) == "" {
		input.Source.Label = "Ручная запись"
	}
	input.Source.Excerpt = truncateRunes(strings.TrimSpace(input.Source.Excerpt), 2000)
	entry := &domainmemory.Entry{
		Title: strings.TrimSpace(input.Title), Content: strings.TrimSpace(input.Content),
		Category: input.Category, Certainty: input.Certainty, Confidence: *input.Confidence,
		Status: domainmemory.StatusActive, Entities: normalizeEntities(input.Entities),
		DocumentSHA: strings.TrimSpace(input.DocumentSHA), Sources: []domainmemory.Source{input.Source},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	entry.NormalizedTitle = normalizeTitle(entry.Title)
	entry.DedupeKey = dedupeKey(entry)
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

func candidateInput(candidate Candidate, source domainmemory.Source) CreateInput {
	confidence := candidate.Confidence
	if candidate.Certainty == "" {
		candidate.Certainty = domainmemory.CertaintyFact
	}
	return CreateInput{Title: candidate.Title, Content: candidate.Content, Category: candidate.Category, Certainty: candidate.Certainty, Confidence: &confidence, Entities: candidate.Entities, Source: source}
}

func canAutomaticallyReplace(entry *domainmemory.Entry) bool {
	if entry.Certainty != domainmemory.CertaintyFact || entry.Confidence < 0.85 {
		return false
	}
	return entry.Sources[0].Kind == domainmemory.SourceUser || entry.Sources[0].Kind == domainmemory.SourceDocument || entry.Confidence >= 0.9
}

func dedupeKey(entry *domainmemory.Entry) string {
	normalized := normalizeContent(entry.Content)
	hash := sha256.Sum256([]byte(string(entry.Area()) + "|" + string(entry.Category) + "|" + entry.NormalizedTitle + "|" + normalized))
	return hex.EncodeToString(hash[:])
}

func normalizeTitle(value string) string   { return normalize(value) }
func normalizeContent(value string) string { return normalize(value) }
func normalize(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}), " ")
}

func normalizeEntities(values []string) []string {
	seen := make(map[string]string, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; !ok {
			seen[key] = value
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result
}

func documentEntities(text string) []string {
	entities := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			entities = append(entities, strings.TrimSpace(strings.TrimLeft(line, "#")))
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "#") && len([]rune(field)) > 1 {
				entities = append(entities, strings.TrimPrefix(field, "#"))
			}
		}
		if len(entities) >= 30 {
			break
		}
	}
	if len(entities) > 30 {
		entities = entities[:30]
	}
	return normalizeEntities(entities)
}

func hasSource(sources []domainmemory.Source, candidate domainmemory.Source) bool {
	for _, source := range sources {
		if source.Kind == candidate.Kind && source.SourceID == candidate.SourceID && source.Excerpt == candidate.Excerpt {
			return true
		}
	}
	return false
}

func appendNewSource(sources []domainmemory.Source, source domainmemory.Source) []domainmemory.Source {
	if hasSource(sources, source) {
		return sources
	}
	return append(sources, source)
}

func page(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func float64Ptr(value float64) *float64 { return &value }

func (s *Service) publish(eventType string, data any) {
	if s.publisher != nil && eventType != "" {
		if err := s.publisher.Publish(eventType, data); err != nil {
			// The event did not reach the stream; the durable change itself
			// already committed, so the failure is reported, not propagated.
			slog.Error("publish event failed", "type", eventType, "error", err)
		}
	}
}
