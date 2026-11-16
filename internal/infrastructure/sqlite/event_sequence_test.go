package sqlite

import (
	"context"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/events"
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
	epoch1, start, err := first.Reserve(ctx, block)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if start != 1 {
		t.Fatalf("first range starts at %d, want 1", start)
	}
	if epoch1 == 0 {
		t.Fatal("epoch must never be zero: zero means 'no durable provenance'")
	}

	// A restart builds a new allocator over the same database.
	second := NewEventSequence(db)
	epoch2, next, err := second.Reserve(ctx, block)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if next <= start+block-1 {
		t.Fatalf("second range starts at %d, which overlaps the first (%d..%d)", next, start, start+block-1)
	}
	if epoch2 == epoch1 {
		t.Fatal("a restored database must produce a different epoch, otherwise a client holding old ids cannot be told apart")
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
	if _, _, err := second.Reserve(ctx, 0); err == nil {
		t.Fatal("reserve with an empty block should fail")
	}
}

// TestExhaustedBlockIsRenewedNotReused is the reviewer's exact reproduction: a
// process reserves 1..4096, publishes 4097 events, restarts — and the new
// process must not hand out 4097 again. The allocator itself only advances, so
// this test drives two brokers through one durable counter and checks that the
// second process can never hand out an id the first one already used.
func TestExhaustedBlockIsRenewedNotReused(t *testing.T) {
	db := setupProductionDB(t)
	defer db.Close()

	const block = 4096
	seq := NewEventSequence(db)

	// The first process publishes more events than its block holds: the broker
	// renews the range mid-flight instead of leaving it.
	reserve := seq.Reserve
	first, err := events.NewBrokerReserved(64, block, reserve)
	if err != nil {
		t.Fatalf("first broker: %v", err)
	}
	published := block + 1
	used := make(map[uint64]bool)
	seen := uint64(0) // the last id this process already accounted for
	for i := 0; i < published; i++ {
		if err := first.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("first process publish %d: %v", i, err)
		}
		for _, event := range first.Replay(seen) {
			if used[event.ID] {
				t.Fatalf("first process handed out id %d twice", event.ID)
			}
			used[event.ID] = true
			if event.ID > seen {
				seen = event.ID
			}
		}
	}
	if !used[uint64(published)] {
		t.Fatalf("the last published id %d is missing from the first process", published)
	}

	// The process restarts: a fresh broker over the same durable counter.
	second, err := events.NewBrokerReserved(64, block, reserve)
	if err != nil {
		t.Fatalf("second broker: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := second.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("second process publish %d: %v", i, err)
		}
	}
	for _, event := range second.Replay(0) {
		if used[event.ID] {
			t.Fatalf("the restarted process reused id %d", event.ID)
		}
		used[event.ID] = true
	}
	// After one exhausted block the second process starts above everything the
	// first one handed out, even though the first published past its block.
	wantStart := 2*block + 1
	if got := second.Replay(0)[0].ID; got != uint64(wantStart) {
		t.Fatalf("restarted process starts at %d, want %d", got, wantStart)
	}
}
