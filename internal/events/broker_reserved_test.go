package events

import (
	"context"
	"errors"
	"testing"
)

// countingReserve simulates the durable SQLite allocator: each call hands out
// the next sequential block. It fails once when told to, like a database that
// briefly cannot answer.
type countingReserve struct {
	next   uint64
	block  uint64
	calls  int
	failAt int
}

func newCountingReserve(block uint64) *countingReserve {
	return &countingReserve{next: 1, block: block}
}

func (r *countingReserve) reserve(ctx context.Context, block uint64) (uint64, uint64, error) {
	r.calls++
	if r.failAt > 0 && r.calls == r.failAt {
		return 0, 0, errors.New("database unavailable")
	}
	start := r.next
	epoch := uint64(1000 + r.calls)
	r.next += block
	return epoch, start, nil
}

// TestReservedBrokerNeverLeavesItsRange is the review's P1: the process may
// publish more events than its first block holds. The broker must renew the
// range before the next id would fall outside it, so ids stay inside what the
// durable counter reserved.
func TestReservedBrokerNeverLeavesItsRange(t *testing.T) {
	const block = 4
	reserve := newCountingReserve(block)
	broker, err := NewBrokerReserved(16, block, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}

	// Publish more than one block: each renewal must come from the reserve
	// function, never from incrementing past the reserved range.
	total := block*3 + 1
	var ids []uint64
	for i := 0; i < total; i++ {
		if err := broker.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	replayed := broker.Replay(0)
	if len(replayed) != total {
		t.Fatalf("replay holds %d events, want %d", len(replayed), total)
	}
	for _, event := range replayed {
		ids = append(ids, event.ID)
	}
	// Three reserves: blocks 1..4, 5..8, 9..12, 13..16 — ids stay contiguous.
	want := make([]uint64, total)
	for i := range want {
		want[i] = uint64(i + 1)
	}
	for i := range ids {
		if ids[i] != want[i] {
			t.Fatalf("id %d = %d, want %d (ids: %v)", i, ids[i], want[i], ids)
		}
	}
	if reserve.calls != 4 {
		t.Fatalf("reserve calls = %d, want 4 (one per exhausted block)", reserve.calls)
	}
}

// TestReservedBrokerRefusesEventsWhenTheCounterFails checks that an exhausted
// block with a failing reservation does not publish an event with a reused or
// out-of-range id. The event is refused, the broker state is untouched, and a
// later successful reservation resumes publishing.
func TestReservedBrokerRefusesEventsWhenTheCounterFails(t *testing.T) {
	const block = 2
	reserve := newCountingReserve(block)
	reserve.failAt = 2 // the second renewal fails
	broker, err := NewBrokerReserved(8, block, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}

	// Exhaust the first block: ids 1 and 2.
	if err := broker.Publish("a", nil); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := broker.Publish("b", nil); err != nil {
		t.Fatalf("second publish: %v", err)
	}

	// The next publish needs a new block and the reserve fails.
	if err := broker.Publish("c", nil); err == nil {
		t.Fatal("publish succeeded although the id reservation failed")
	}
	if last := broker.LastID(); last != 2 {
		t.Fatalf("LastID = %d after a refused event, want 2", last)
	}
	if replayed := broker.Replay(2); len(replayed) != 0 {
		t.Fatalf("a refused event must not reach the ring: %+v", replayed)
	}

	// The database recovers; publishing resumes at the next durable range and
	// the ids 1 and 2 are never reused.
	reserve.failAt = 0
	if err := broker.Publish("c", nil); err != nil {
		t.Fatalf("publish after recovery: %v", err)
	}
	replayed := broker.Replay(2)
	if len(replayed) != 1 || replayed[0].ID != 3 {
		t.Fatalf("event after recovery = %+v, want id 3", replayed)
	}
}

// TestEpochChangesWithEveryRange pins the epoch contract: each durable range
// carries a fresh epoch, so a browser holding ids from a restored older database
// can never be mistaken for a current one.
func TestEpochChangesWithEveryRange(t *testing.T) {
	const block = 2
	reserve := newCountingReserve(block)
	broker, err := NewBrokerReserved(8, block, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}

	first := broker.Epoch()
	if first == 0 {
		t.Fatal("a reserved broker always has an epoch")
	}
	if err := broker.Publish("a", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := broker.Epoch(); got != first {
		t.Fatalf("epoch changed without a renewal: %d -> %d", first, got)
	}

	// Publish past the block: the renewal must stamp the new epoch on the
	// events it issues from now on.
	if err := broker.Publish("b", nil); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if err := broker.Publish("c", nil); err != nil {
		t.Fatalf("publish 3: %v", err)
	}
	second := broker.Epoch()
	if second == first {
		t.Fatal("a renewed range must carry a fresh epoch")
	}
	replayed := broker.Replay(2)
	if len(replayed) != 1 || replayed[0].Epoch != second {
		t.Fatalf("the renewed event must carry the new epoch, got %+v", replayed)
	}
}

// TestUnboundedBrokerKeepsNumericIDs documents the test-only mode: a broker
// built without a reservation has no epoch, and its ids stay plain numbers so
// the SSE wire format does not change for existing tests.
func TestUnboundedBrokerKeepsNumericIDs(t *testing.T) {
	broker := NewBrokerAt(1, 4)
	if epoch := broker.Epoch(); epoch != 0 {
		t.Fatalf("epoch = %d, want 0 for a broker without durable ids", epoch)
	}
	if err := broker.Publish("x", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if replayed := broker.Replay(0); len(replayed) != 1 || replayed[0].Epoch != 0 || replayed[0].ID != 1 {
		t.Fatalf("event = %+v, want epoch 0 id 1", replayed)
	}
}

// TestNewBrokerReservedRejectsInvalidSetup guards the fail-fast contract: a
// misconfigured or failing reservation must never produce a broker that could
// reuse ids.
func TestNewBrokerReservedRejectsInvalidSetup(t *testing.T) {
	if _, err := NewBrokerReserved(4, 0, newCountingReserve(4).reserve); err == nil {
		t.Fatal("a zero block must be rejected")
	}
	if _, err := NewBrokerReserved(4, 4, nil); err == nil {
		t.Fatal("a missing reserve function must be rejected")
	}
	failing := newCountingReserve(4)
	failing.failAt = 1
	if _, err := NewBrokerReserved(4, 4, failing.reserve); err == nil {
		t.Fatal("a failing first reservation must be rejected")
	}
	// A stale range that overlaps ids the broker has already used must be
	// refused too, which is what a restored older database would look like.
	overlapping := &countingReserve{next: 1, block: 4}
	broker, err := NewBrokerReserved(4, 4, overlapping.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := broker.Publish("x", nil); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	// The simulated restore hands out the same range again.
	overlapping.next = 1
	if err := broker.Publish("x", nil); err == nil {
		t.Fatal("a reservation overlapping used ids must be refused")
	}
}
