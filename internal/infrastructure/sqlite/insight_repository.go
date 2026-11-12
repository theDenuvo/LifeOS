package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/proactive"
)

// InsightUnitOfWork implements proactive.UnitOfWork on top of SQLite.
type InsightUnitOfWork struct {
	db *sql.DB
}

// NewInsightUnitOfWork builds an insight unit of work.
func NewInsightUnitOfWork(db *sql.DB) *InsightUnitOfWork {
	return &InsightUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *InsightUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx proactive.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, taskTransaction{tx: tx})
	})
}

// insightRepository is the SQLite implementation of proactive.Repository.
type insightRepository struct {
	tx *sql.Tx
}

// ListStates returns stored decisions for the requested keys.
func (r *insightRepository) ListStates(ctx context.Context, keys []string) (map[string]proactive.State, error) {
	states := make(map[string]proactive.State, len(keys))
	if len(keys) == 0 {
		return states, nil
	}

	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, key := range keys {
		placeholders[i] = "?"
		args[i] = key
	}

	rows, err := r.tx.QueryContext(ctx,
		`SELECT key, dismissed_at, snoozed_until FROM insight_states WHERE key IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("list insight states: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key          string
			dismissedAt  sql.NullInt64
			snoozedUntil sql.NullInt64
		)
		if err := rows.Scan(&key, &dismissedAt, &snoozedUntil); err != nil {
			return nil, fmt.Errorf("scan insight state: %w", err)
		}
		state := proactive.State{Key: key}
		if dismissedAt.Valid {
			value := timeFromUnix(dismissedAt.Int64)
			state.DismissedAt = &value
		}
		if snoozedUntil.Valid {
			value := timeFromUnix(snoozedUntil.Int64)
			state.SnoozedUntil = &value
		}
		states[key] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan insight states: %w", err)
	}
	return states, nil
}

// Dismiss records a dismissal, clearing any snooze.
func (r *insightRepository) Dismiss(ctx context.Context, key string, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		INSERT INTO insight_states (key, dismissed_at, snoozed_until, updated_at)
		VALUES (?, ?, NULL, ?)
		ON CONFLICT(key) DO UPDATE SET
			dismissed_at = excluded.dismissed_at,
			snoozed_until = NULL,
			updated_at = excluded.updated_at`,
		key, unixSeconds(at), unixSeconds(at))
	if err != nil {
		return fmt.Errorf("dismiss insight %q: %w", key, err)
	}
	return nil
}

// Snooze records a snooze, clearing any dismissal.
func (r *insightRepository) Snooze(ctx context.Context, key string, until, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		INSERT INTO insight_states (key, dismissed_at, snoozed_until, updated_at)
		VALUES (?, NULL, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			dismissed_at = NULL,
			snoozed_until = excluded.snoozed_until,
			updated_at = excluded.updated_at`,
		key, unixSeconds(until), unixSeconds(at))
	if err != nil {
		return fmt.Errorf("snooze insight %q: %w", key, err)
	}
	return nil
}
