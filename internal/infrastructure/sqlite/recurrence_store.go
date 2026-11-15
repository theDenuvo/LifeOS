package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// RecurrenceStore implements recurrence.Store on SQLite.
//
// Pending work is derived from the task table itself, so nothing has to be
// enqueued at completion time and a crash between "completed" and "next
// occurrence created" cannot lose the recurrence.
type RecurrenceStore struct {
	db *sql.DB
}

// NewRecurrenceStore builds the store over an open database.
func NewRecurrenceStore(db *sql.DB) *RecurrenceStore {
	return &RecurrenceStore{db: db}
}

// PendingParents returns completed recurring tasks without a next occurrence.
func (s *RecurrenceStore) PendingParents(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id FROM tasks t
		WHERE t.status = 'completed'
		  AND t.repeat IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM tasks child WHERE child.repeat_parent_id = t.id)
		ORDER BY t.completed_at DESC, t.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending recurrences: %w", err)
	}
	defer rows.Close()

	out := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pending recurrence: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending recurrences: %w", err)
	}
	return out, nil
}

// ChildExists reports whether the next occurrence of a task was created.
func (s *RecurrenceStore) ChildExists(ctx context.Context, parentID int64) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM tasks WHERE repeat_parent_id = ? LIMIT 1`, parentID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check next occurrence: %w", err)
	}
	return true, nil
}
