package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/completion"
)

// CompletionStore persists completion checks on SQLite.
type CompletionStore struct {
	db *sql.DB
}

// NewCompletionStore builds the store over an open database.
func NewCompletionStore(db *sql.DB) *CompletionStore {
	return &CompletionStore{db: db}
}

// Save records one check and returns its id.
func (s *CompletionStore) Save(ctx context.Context, report completion.Report) (int64, error) {
	evidence, err := json.Marshal(report.Evidence)
	if err != nil {
		return 0, fmt.Errorf("encode completion evidence: %w", err)
	}
	sources, err := json.Marshal(report.Sources)
	if err != nil {
		return 0, fmt.Errorf("encode completion sources: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO completion_checks (task_id, verdict, confidence, reason, evidence, sources, decision, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		report.TaskID,
		string(report.Verdict),
		report.Confidence,
		report.Reason,
		string(evidence),
		string(sources),
		completion.DecisionNone,
		unixSeconds(report.CheckedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("save completion check: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save completion check: read id: %w", err)
	}
	return id, nil
}

// SetDecision records what the user decided about a check.
func (s *CompletionStore) SetDecision(ctx context.Context, id int64, decision string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE completion_checks SET decision = ?, decided_at = ? WHERE id = ?`,
		decision, unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("record completion decision: %w", err)
	}
	return nil
}

// Declined reports whether the user refused completion for the task before.
func (s *CompletionStore) Declined(ctx context.Context, taskID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM completion_checks WHERE task_id = ? AND decision = 'declined'`,
		taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read completion decision: %w", err)
	}
	return count > 0, nil
}

// Latest returns the most recent check of a task.
func (s *CompletionStore) Latest(ctx context.Context, taskID int64) (completion.Report, bool, error) {
	var (
		id         int64
		storedID   int64
		verdict    string
		confidence float64
		reason     string
		evidence   sql.NullString
		sources    sql.NullString
		decision   string
		createdAt  int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, verdict, confidence, reason, evidence, sources, decision, created_at
		FROM completion_checks WHERE task_id = ? ORDER BY id DESC LIMIT 1`, taskID).
		Scan(&id, &storedID, &verdict, &confidence, &reason, &evidence, &sources, &decision, &createdAt)
	if err == sql.ErrNoRows {
		return completion.Report{}, false, nil
	}
	if err != nil {
		return completion.Report{}, false, fmt.Errorf("read completion check: %w", err)
	}
	report := completion.Report{
		ID:         id,
		TaskID:     storedID,
		Verdict:    completion.Verdict(verdict),
		Confidence: confidence,
		Reason:     reason,
		Decision:   decision,
		CheckedAt:  timeFromUnix(createdAt),
		Evidence:   []completion.Evidence{},
		Sources:    []completion.SourceStatus{},
	}
	if evidence.Valid {
		_ = json.Unmarshal([]byte(evidence.String), &report.Evidence)
	}
	if sources.Valid {
		_ = json.Unmarshal([]byte(sources.String), &report.Sources)
	}
	return report, true, nil
}

// DocumentSearcher implements completion.DocumentSearcher over the documents table.
type DocumentSearcher struct {
	db *sql.DB
}

// NewDocumentSearcher builds the searcher over an open database.
func NewDocumentSearcher(db *sql.DB) *DocumentSearcher {
	return &DocumentSearcher{db: db}
}

// SearchText returns documents whose extracted text mentions any probe.
//
// The search is a literal substring match, not a word overlap: a document that
// contains the phrase the user wrote as the expected result is real evidence, a
// document that happens to share a word is not.
func (s *DocumentSearcher) SearchText(ctx context.Context, probes []string, since time.Time, limit int) ([]completion.DocumentHit, error) {
	if len(probes) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	conditions := make([]string, 0, len(probes))
	args := make([]any, 0, len(probes)+2)
	args = append(args, unixSeconds(since))
	for _, probe := range probes {
		conditions = append(conditions, "instr(lower(extracted_text), lower(?)) > 0")
		args = append(args, probe)
	}
	args = append(args, limit)
	query := `
		SELECT id, filename, extracted_text, updated_at FROM documents
		WHERE status = 'processed' AND extracted_text IS NOT NULL
		  AND updated_at >= ? AND (` + strings.Join(conditions, " OR ") + `)
		ORDER BY updated_at DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	defer rows.Close()

	out := make([]completion.DocumentHit, 0, limit)
	for rows.Next() {
		var (
			id        int64
			filename  string
			text      string
			updatedAt int64
		)
		if err := rows.Scan(&id, &filename, &text, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan document hit: %w", err)
		}
		out = append(out, completion.DocumentHit{
			ID:        id,
			Filename:  filename,
			Excerpt:   excerpt(text, probes),
			UpdatedAt: timeFromUnix(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	return out, nil
}

// excerpt returns a short window around the first probe that matched.
func excerpt(text string, probes []string) string {
	const radius = 160
	lower := strings.ToLower(text)
	index := -1
	for _, probe := range probes {
		found := strings.Index(lower, strings.ToLower(probe))
		if found >= 0 && (index < 0 || found < index) {
			index = found
		}
	}
	if index < 0 {
		if len(text) > radius {
			return text[:radius]
		}
		return text
	}
	start := index - radius
	if start < 0 {
		start = 0
	}
	end := index + radius
	if end > len(text) {
		end = len(text)
	}
	return strings.TrimSpace(text[start:end])
}
