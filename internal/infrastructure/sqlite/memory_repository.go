package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

// MemoryUnitOfWork implements memory.UnitOfWork on SQLite.
type MemoryUnitOfWork struct{ db *sql.DB }

func NewMemoryUnitOfWork(db *sql.DB) *MemoryUnitOfWork { return &MemoryUnitOfWork{db: db} }

func (u *MemoryUnitOfWork) Do(ctx context.Context, fn func(context.Context, appmemory.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, memoryTransaction{tx: tx})
	})
}

type memoryTransaction struct{ tx *sql.Tx }

func (t memoryTransaction) Memory() appmemory.Repository { return &memoryRepository{tx: t.tx} }

type memoryRepository struct{ tx *sql.Tx }

const memoryColumns = `id, area, category, title, normalized_title, content, dedupe_key,
	COALESCE(document_sha, ''), certainty, confidence, status, superseded_by, entities,
	created_at, updated_at, version`

// memoryColumnsE is the same list qualified for queries that join memory_fts.
const memoryColumnsE = `e.id, e.area, e.category, e.title, e.normalized_title, e.content, e.dedupe_key,
	COALESCE(e.document_sha, ''), e.certainty, e.confidence, e.status, e.superseded_by, e.entities,
	e.created_at, e.updated_at, e.version`

// InsertEntry stores one entry with its provenance.
func (r *memoryRepository) InsertEntry(ctx context.Context, entry *domainmemory.Entry) (int64, error) {
	if err := entry.Validate(); err != nil {
		return 0, err
	}
	entities, err := encodeEntities(entry.Entities)
	if err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO memory_entries (
			area, category, title, normalized_title, content, dedupe_key, document_sha,
			certainty, confidence, status, entities, created_at, updated_at, version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		string(entry.Area()), string(entry.Category), entry.Title, entry.NormalizedTitle,
		entry.Content, entry.DedupeKey, nullableString(entry.DocumentSHA), string(entry.Certainty),
		entry.Confidence, string(entry.Status), entities,
		unixSeconds(entry.CreatedAt), unixSeconds(entry.UpdatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert memory entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert memory entry: read id: %w", err)
	}
	for _, source := range entry.Sources {
		if err := r.insertSource(ctx, id, source); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// GetEntry loads one entry with its sources.
func (r *memoryRepository) GetEntry(ctx context.Context, id int64) (*domainmemory.Entry, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memory_entries WHERE id = ?`, id)
	entry, err := scanMemoryEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appmemory.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get memory entry %d: %w", id, err)
	}
	sources, err := r.listSources(ctx, id)
	if err != nil {
		return nil, err
	}
	entry.Sources = sources
	return entry, nil
}

// UpdateEntry performs a conditional write and bumps the version.
func (r *memoryRepository) UpdateEntry(ctx context.Context, id int64, expectedVersion int, entry *domainmemory.Entry) (*domainmemory.Entry, error) {
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	entities, err := encodeEntities(entry.Entities)
	if err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE memory_entries SET
			area = ?, category = ?, title = ?, normalized_title = ?, content = ?, dedupe_key = ?,
			certainty = ?, confidence = ?, status = ?, entities = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		string(entry.Area()), string(entry.Category), entry.Title, entry.NormalizedTitle,
		entry.Content, entry.DedupeKey, string(entry.Certainty), entry.Confidence,
		string(entry.Status), entities, unixSeconds(entry.UpdatedAt), id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update memory entry %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update memory entry %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.GetEntry(ctx, id); errors.Is(err, appmemory.ErrNotFound) {
			return nil, appmemory.ErrNotFound
		}
		return nil, appmemory.ErrVersionConflict
	}
	return r.GetEntry(ctx, id)
}

// SetStatus changes lifecycle state under optimistic locking.
func (r *memoryRepository) SetStatus(ctx context.Context, id int64, expectedVersion int, status domainmemory.Status, supersededBy *int64, at time.Time) (*domainmemory.Entry, error) {
	var target any
	if supersededBy != nil {
		target = *supersededBy
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE memory_entries SET status = ?, superseded_by = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		string(status), target, unixSeconds(at), id, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("set memory status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		if _, err := r.GetEntry(ctx, id); errors.Is(err, appmemory.ErrNotFound) {
			return nil, appmemory.ErrNotFound
		}
		return nil, appmemory.ErrVersionConflict
	}
	return r.GetEntry(ctx, id)
}

func (r *memoryRepository) DeleteEntry(ctx context.Context, id int64, expectedVersion int) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM memory_entries WHERE id = ? AND version = ?`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete memory entry %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, err := r.GetEntry(ctx, id); errors.Is(err, appmemory.ErrNotFound) {
			return appmemory.ErrNotFound
		}
		return appmemory.ErrVersionConflict
	}
	return nil
}

// ListEntries filters by area, category, status and full-text query. A query
// uses FTS5 with a prefix match on the last token, which behaves like search as
// the user types rather than requiring whole words.
func (r *memoryRepository) ListEntries(ctx context.Context, filter appmemory.Filter) ([]*domainmemory.Entry, int, error) {
	conditions := make([]string, 0, 5)
	args := make([]any, 0, 6)
	from := `FROM memory_entries e`

	if filter.Query != "" {
		match := ftsQuery(filter.Query)
		if match != "" {
			from += ` JOIN memory_fts f ON f.rowid = e.id`
			conditions = append(conditions, `memory_fts MATCH ?`)
			args = append(args, match)
		}
	}
	if filter.Area != nil {
		conditions = append(conditions, `e.area = ?`)
		args = append(args, string(*filter.Area))
	}
	if filter.Category != nil {
		conditions = append(conditions, `e.category = ?`)
		args = append(args, string(*filter.Category))
	}
	if filter.Status != nil {
		conditions = append(conditions, `e.status = ?`)
		args = append(args, string(*filter.Status))
	} else if !filter.IncludeDisputed {
		conditions = append(conditions, `e.status <> 'superseded'`)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) `+from+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count memory entries: %w", err)
	}

	query := `SELECT ` + memoryColumnsE + ` ` + from + where + ` ORDER BY e.updated_at DESC, e.id DESC LIMIT ? OFFSET ?`
	rows, err := r.tx.QueryContext(ctx, query, append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list memory entries: %w", err)
	}
	defer rows.Close()

	entries := make([]*domainmemory.Entry, 0, filter.Limit)
	for rows.Next() {
		entry, err := scanMemoryEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, entry := range entries {
		sources, err := r.listSources(ctx, entry.ID)
		if err != nil {
			return nil, 0, err
		}
		entry.Sources = sources
	}
	return entries, total, nil
}

// FindSameSubject returns active entries that talk about the same thing, so a
// newer claim can be compared instead of accumulating duplicates.
func (r *memoryRepository) FindSameSubject(ctx context.Context, area domainmemory.Area, category domainmemory.Category, normalizedTitle string) ([]*domainmemory.Entry, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+memoryColumns+` FROM memory_entries
		WHERE area = ? AND category = ? AND normalized_title = ? AND status IN ('active','disputed')
		ORDER BY updated_at DESC, id DESC LIMIT 5`,
		string(area), string(category), normalizedTitle)
	if err != nil {
		return nil, fmt.Errorf("find same memory subject: %w", err)
	}
	defer rows.Close()
	entries := make([]*domainmemory.Entry, 0, 4)
	for rows.Next() {
		entry, err := scanMemoryEntry(rows)
		if err != nil {
			return nil, err
		}
		sources, err := r.listSources(ctx, entry.ID)
		if err != nil {
			return nil, err
		}
		entry.Sources = sources
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// FindByDedupeKey locates an exact duplicate that is still current.
func (r *memoryRepository) FindByDedupeKey(ctx context.Context, key string) (*domainmemory.Entry, error) {
	row := r.tx.QueryRowContext(ctx, `
		SELECT `+memoryColumns+` FROM memory_entries
		WHERE dedupe_key = ? AND status <> 'superseded'
		ORDER BY id DESC LIMIT 1`, key)
	entry, err := scanMemoryEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sources, err := r.listSources(ctx, entry.ID)
	if err != nil {
		return nil, err
	}
	entry.Sources = sources
	return entry, nil
}

// AddSource appends provenance unless it is already recorded.
func (r *memoryRepository) AddSource(ctx context.Context, entryID int64, source domainmemory.Source) error {
	return r.insertSource(ctx, entryID, source)
}

func (r *memoryRepository) insertSource(ctx context.Context, entryID int64, source domainmemory.Source) error {
	_, err := r.tx.ExecContext(ctx, `
		INSERT INTO memory_sources (entry_id, kind, source_id, label, excerpt, observed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entryID, string(source.Kind), source.SourceID, source.Label, source.Excerpt,
		unixSeconds(source.ObservedAt), unixSeconds(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("insert memory source: %w", err)
	}
	return nil
}

func (r *memoryRepository) listSources(ctx context.Context, entryID int64) ([]domainmemory.Source, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT id, kind, source_id, label, excerpt, observed_at
		FROM memory_sources WHERE entry_id = ? ORDER BY observed_at DESC, id DESC`, entryID)
	if err != nil {
		return nil, fmt.Errorf("list memory sources: %w", err)
	}
	defer rows.Close()
	sources := make([]domainmemory.Source, 0, 2)
	for rows.Next() {
		var (
			id         int64
			kind       string
			sourceID   int64
			label      string
			excerpt    string
			observedAt int64
		)
		if err := rows.Scan(&id, &kind, &sourceID, &label, &excerpt, &observedAt); err != nil {
			return nil, err
		}
		sources = append(sources, domainmemory.Source{
			ID: id, Kind: domainmemory.SourceKind(kind), SourceID: sourceID,
			Label: label, Excerpt: excerpt, ObservedAt: timeFromUnix(observedAt),
		})
	}
	return sources, rows.Err()
}

func (r *memoryRepository) Stats(ctx context.Context) (appmemory.Stats, error) {
	var stats appmemory.Stats
	err := r.tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN area = 'personal' AND status <> 'superseded' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN area = 'knowledge' AND status <> 'superseded' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM memory_conflicts WHERE status = 'pending')
		FROM memory_entries`).Scan(&stats.Personal, &stats.Knowledge, &stats.PendingConflicts)
	if err != nil {
		return appmemory.Stats{}, fmt.Errorf("memory stats: %w", err)
	}
	return stats, nil
}

func (r *memoryRepository) InsertLink(ctx context.Context, link *domainmemory.Link) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO memory_links (entry_id, target_type, target_id, relation, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(entry_id, target_type, target_id, relation) DO NOTHING`,
		link.EntryID, string(link.TargetType), link.TargetID, link.Relation, unixSeconds(link.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert memory link: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		if err := r.tx.QueryRowContext(ctx, `
			SELECT id FROM memory_links WHERE entry_id = ? AND target_type = ? AND target_id = ? AND relation = ?`,
			link.EntryID, string(link.TargetType), link.TargetID, link.Relation).Scan(&id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (r *memoryRepository) ListLinks(ctx context.Context, entryID int64) ([]domainmemory.Link, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT id, entry_id, target_type, target_id, relation, created_at
		FROM memory_links WHERE entry_id = ? ORDER BY id ASC`, entryID)
	if err != nil {
		return nil, fmt.Errorf("list memory links: %w", err)
	}
	defer rows.Close()
	links := make([]domainmemory.Link, 0, 4)
	for rows.Next() {
		var (
			id         int64
			entry      int64
			targetType string
			targetID   int64
			relation   string
			createdAt  int64
		)
		if err := rows.Scan(&id, &entry, &targetType, &targetID, &relation, &createdAt); err != nil {
			return nil, err
		}
		links = append(links, domainmemory.Link{
			ID: id, EntryID: entry, TargetType: domainmemory.TargetType(targetType),
			TargetID: targetID, Relation: relation, CreatedAt: timeFromUnix(createdAt),
		})
	}
	return links, rows.Err()
}

func (r *memoryRepository) DeleteLink(ctx context.Context, id, entryID int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM memory_links WHERE id = ? AND entry_id = ?`, id, entryID)
	if err != nil {
		return fmt.Errorf("delete memory link: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return appmemory.ErrLinkNotFound
	}
	return nil
}

func (r *memoryRepository) InsertConflict(ctx context.Context, conflict *domainmemory.Conflict) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO memory_conflicts (existing_id, candidate_id, reason, status, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		conflict.ExistingID, conflict.CandidateID, conflict.Reason, string(conflict.Status), unixSeconds(conflict.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert memory conflict: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *memoryRepository) GetConflict(ctx context.Context, id int64) (*domainmemory.Conflict, error) {
	row := r.tx.QueryRowContext(ctx, `
		SELECT id, existing_id, candidate_id, reason, status, COALESCE(resolution, ''), created_at, resolved_at
		FROM memory_conflicts WHERE id = ?`, id)
	conflict, err := scanConflict(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appmemory.ErrConflictNotFound
	}
	if err != nil {
		return nil, err
	}
	return conflict, nil
}

func (r *memoryRepository) ListConflicts(ctx context.Context, pendingOnly bool) ([]*domainmemory.Conflict, error) {
	query := `
		SELECT id, existing_id, candidate_id, reason, status, COALESCE(resolution, ''), created_at, resolved_at
		FROM memory_conflicts`
	if pendingOnly {
		query += ` WHERE status = 'pending'`
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT 100`
	rows, err := r.tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list memory conflicts: %w", err)
	}
	defer rows.Close()
	conflicts := make([]*domainmemory.Conflict, 0, 4)
	for rows.Next() {
		conflict, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func (r *memoryRepository) ResolveConflict(ctx context.Context, id int64, resolution string, at time.Time) error {
	result, err := r.tx.ExecContext(ctx, `
		UPDATE memory_conflicts SET status = 'resolved', resolution = ?, resolved_at = ?
		WHERE id = ? AND status = 'pending'`, resolution, unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("resolve memory conflict: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return appmemory.ErrConflictResolved
	}
	return nil
}

// TargetExists verifies a link target using the current transaction. A second
// connection would deadlock: SQLite here runs with a single pooled connection,
// so a nested query outside the transaction waits for a lock it already holds.
func (r *memoryRepository) TargetExists(ctx context.Context, target domainmemory.TargetType, id int64) (bool, error) {
	table := ""
	switch target {
	case domainmemory.TargetMemory:
		table = "memory_entries"
	case domainmemory.TargetTask:
		table = "tasks"
	case domainmemory.TargetEvent:
		table = "calendar_events"
	case domainmemory.TargetConversation:
		table = "conversations"
	case domainmemory.TargetDocument:
		table = "documents"
	case domainmemory.TargetInbox:
		table = "inbox_items"
	default:
		// Goals have no module yet, so a link to one cannot be verified.
		return false, nil
	}
	var found int
	err := r.tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check memory link target: %w", err)
	}
	return true, nil
}

func scanMemoryEntry(row rowScanner) (*domainmemory.Entry, error) {
	var (
		id              int64
		area            string
		category        string
		title           string
		normalizedTitle string
		content         string
		dedupeKey       string
		documentSHA     string
		certainty       string
		confidence      float64
		status          string
		supersededBy    sql.NullInt64
		entities        string
		createdAt       int64
		updatedAt       int64
		version         int
	)
	if err := row.Scan(&id, &area, &category, &title, &normalizedTitle, &content, &dedupeKey,
		&documentSHA, &certainty, &confidence, &status, &supersededBy, &entities,
		&createdAt, &updatedAt, &version); err != nil {
		return nil, err
	}
	entry := &domainmemory.Entry{
		ID: id, Title: title, NormalizedTitle: normalizedTitle, Content: content,
		DedupeKey: dedupeKey, DocumentSHA: documentSHA,
		Category: domainmemory.Category(category), Certainty: domainmemory.Certainty(certainty),
		Confidence: confidence, Status: domainmemory.Status(status),
		CreatedAt: timeFromUnix(createdAt), UpdatedAt: timeFromUnix(updatedAt), Version: version,
	}
	if supersededBy.Valid {
		entry.SupersededBy = &supersededBy.Int64
	}
	if err := json.Unmarshal([]byte(entities), &entry.Entities); err != nil {
		entry.Entities = nil
	}
	// area is stored for filtering; it is fully derived from the category, so a
	// mismatch means the row was written by an older or broken writer.
	if domainmemory.Area(area) != entry.Category.Area() {
		return nil, fmt.Errorf("memory entry %d: area %q does not match category %q", id, area, category)
	}
	return entry, nil
}

func scanConflict(row rowScanner) (*domainmemory.Conflict, error) {
	var (
		id          int64
		existingID  int64
		candidateID int64
		reason      string
		status      string
		resolution  string
		createdAt   int64
		resolvedAt  sql.NullInt64
	)
	if err := row.Scan(&id, &existingID, &candidateID, &reason, &status, &resolution, &createdAt, &resolvedAt); err != nil {
		return nil, err
	}
	conflict := &domainmemory.Conflict{
		ID: id, ExistingID: existingID, CandidateID: candidateID, Reason: reason,
		Status: domainmemory.ConflictStatus(status), Resolution: resolution,
		CreatedAt: timeFromUnix(createdAt),
	}
	if resolvedAt.Valid {
		value := timeFromUnix(resolvedAt.Int64)
		conflict.ResolvedAt = &value
	}
	return conflict, nil
}

func encodeEntities(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode memory entities: %w", err)
	}
	return string(encoded), nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// ftsQuery turns user text into an FTS5 prefix query. Every token is quoted so
// punctuation in the query cannot be parsed as FTS syntax.
func ftsQuery(value string) string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == '*' || r == '(' || r == ')' || r == ':' || r == '-'
	})
	tokens := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		token := strings.ToLower(strings.TrimSpace(field))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, `"`+token+`"*`)
		if len(tokens) >= 12 {
			break
		}
	}
	return strings.Join(tokens, " AND ")
}
