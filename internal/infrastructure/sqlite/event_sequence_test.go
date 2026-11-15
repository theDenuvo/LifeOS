package sqlite

import (
	"context"
	"testing"
)

// TestEventSequenceKeepsIDsUniqueAcrossRestarts is the persistence half of the
// SSE restart fix: a new process must never hand out an id that an already
// running browser has seen, because the browser answers with Last-Event-ID.
func TestEventSequenceKeepsIDsUniqueAcrossRestarts(t *testing.T) {
	db := setupProductionDB(t)
	defer db.Close()
	ctx := context.Background()

	const block = 4096
	first := NewEventSequence(db)
	start, err := first.Reserve(ctx, block)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if start != 1 {
		t.Fatalf("first range starts at %d, want 1", start)
	}

	// A restart builds a new allocator over the same database.
	second := NewEventSequence(db)
	next, err := second.Reserve(ctx, block)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if next <= start+block-1 {
		t.Fatalf("second range starts at %d, which overlaps the first (%d..%d)", next, start, start+block-1)
	}

	last, err := second.Last(ctx)
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if last != next+block-1 {
		t.Fatalf("durable counter = %d, want %d", last, next+block-1)
	}

	// Rejecting a zero block keeps the counter honest instead of reserving
	// nothing and returning a bogus start.
	if _, err := second.Reserve(ctx, 0); err == nil {
		t.Fatal("reserve with an empty block should fail")
	}
}
