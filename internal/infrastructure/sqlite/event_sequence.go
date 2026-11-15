package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EventSequence hands out ranges of event ids that stay unique across process
// restarts.
//
// The SSE stream identifies events by id, and a browser remembers the last id it
// saw. If the id counter restarted at zero, every event published after a
// restart would look older than the client's position and be dropped, so the
// counter is persisted and reserved in blocks: one write at startup instead of
// a write per event, with no risk of reusing an id that was already delivered.
type EventSequence struct {
	db *sql.DB
}

// NewEventSequence builds the persisted event id allocator.
func NewEventSequence(db *sql.DB) *EventSequence {
	return &EventSequence{db: db}
}

// Reserve advances the durable counter by block and returns the first id of the
// reserved range. The caller may hand out at most block ids (start .. start+block-1);
// unused ones are skipped, never reused, which is what keeps ids monotonic.
func (s *EventSequence) Reserve(ctx context.Context, block uint64) (uint64, error) {
	if block == 0 {
		return 0, errors.New("reserve event ids: block must be positive")
	}
	var high uint64
	err := s.db.QueryRowContext(ctx,
		`UPDATE event_sequence SET value = value + ? WHERE id = 1 RETURNING value`,
		block,
	).Scan(&high)
	if errors.Is(err, sql.ErrNoRows) {
		// The row is created by the migration; a database without it is still
		// usable, so create the counter instead of failing to publish events.
		if _, insertErr := s.db.ExecContext(ctx,
			`INSERT INTO event_sequence (id, value) VALUES (1, ?)`, block,
		); insertErr != nil {
			return 0, fmt.Errorf("reserve event ids: %w", insertErr)
		}
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reserve event ids: %w", err)
	}
	return high - block + 1, nil
}

// Last returns the highest id handed out so far.
func (s *EventSequence) Last(ctx context.Context) (uint64, error) {
	var value uint64
	err := s.db.QueryRowContext(ctx, `SELECT value FROM event_sequence WHERE id = 1`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read event sequence: %w", err)
	}
	return value, nil
}
