package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
)

// EventSequence hands out ranges of event ids that stay unique across process
// restarts, together with the epoch that identifies the range.
//
// The SSE stream identifies events by id, and a browser remembers the last id it
// saw. If the id counter restarted at zero, every event published after a
// restart would look older than the client's position and be dropped, so the
// counter is persisted and reserved in blocks: one write at startup instead of
// a write per event, with no risk of reusing an id that was already delivered.
//
// The epoch is regenerated on every reservation. A browser stores the epoch its
// ids came from, so a database restored from an older copy produces a different
// epoch and the client is told to resynchronise instead of trusting numbers that
// skip the lost window.
type EventSequence struct {
	db *sql.DB
}

// NewEventSequence builds the persisted event id allocator.
func NewEventSequence(db *sql.DB) *EventSequence {
	return &EventSequence{db: db}
}

// Reserve advances the durable counter by block, generates a fresh epoch for the
// range and returns both. The caller may hand out at most block ids
// (start .. start+block-1); unused ones are skipped, never reused, which is what
// keeps ids monotonic.
func (s *EventSequence) Reserve(ctx context.Context, block uint64) (epoch, start uint64, err error) {
	if block == 0 {
		return 0, 0, errors.New("reserve event ids: block must be positive")
	}
	epoch, err = newEpoch()
	if err != nil {
		return 0, 0, fmt.Errorf("reserve event ids: %w", err)
	}
	var high uint64
	err = s.db.QueryRowContext(ctx,
		`UPDATE event_sequence SET value = value + ?, epoch = ? WHERE id = 1 RETURNING value`,
		block, epoch,
	).Scan(&high)
	if errors.Is(err, sql.ErrNoRows) {
		// The row is created by the migration; a database without it is still
		// usable, so create the counter instead of failing to publish events.
		if _, insertErr := s.db.ExecContext(ctx,
			`INSERT INTO event_sequence (id, value, epoch) VALUES (1, ?, ?)`, block, epoch,
		); insertErr != nil {
			return 0, 0, fmt.Errorf("reserve event ids: %w", insertErr)
		}
		return epoch, 1, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reserve event ids: %w", err)
	}
	return epoch, high - block + 1, nil
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

// newEpoch returns a value that has never been used before, so a restored older
// database can never be confused with the current one.
//
// SQLite cannot bind unsigned values with the high bit set, so the epoch stays
// inside the signed range; 63 random bits are still far beyond any chance of a
// collision.
func newEpoch() (uint64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("generate event epoch: %w", err)
	}
	epoch := binary.LittleEndian.Uint64(raw[:]) >> 1
	if epoch == 0 {
		epoch = 1
	}
	return epoch, nil
}
