package events

import (
	"context"
	"testing"
	"time"
)

func receive(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatal("subscription closed unexpectedly")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestBrokerPublishesToSubscribers(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := broker.Subscribe(ctx)
	_ = broker.Publish("task.created", map[string]any{"id": 1})

	event := receive(t, stream)
	if event.Type != "task.created" || event.ID != 1 {
		t.Fatalf("event = %+v", event)
	}
}

func TestBrokerReplayAfterID(t *testing.T) {
	broker := NewBroker()
	_ = broker.Publish("task.created", map[string]any{"id": 1})
	_ = broker.Publish("task.updated", map[string]any{"id": 1})
	_ = broker.Publish("inbox.captured", map[string]any{"id": 2})

	replayed := broker.Replay(1)
	if len(replayed) != 2 {
		t.Fatalf("replayed %d events, want 2", len(replayed))
	}
	if replayed[0].ID != 2 || replayed[1].ID != 3 {
		t.Errorf("ids = %d, %d", replayed[0].ID, replayed[1].ID)
	}
	if got := broker.LastID(); got != 3 {
		t.Errorf("last id = %d, want 3", got)
	}
}

func TestBrokerReplayBufferIsBounded(t *testing.T) {
	broker := NewBrokerWithReplay(2)
	for i := 0; i < 5; i++ {
		_ = broker.Publish("task.updated", map[string]any{"id": i})
	}
	replayed := broker.Replay(0)
	if len(replayed) != 2 {
		t.Fatalf("replayed %d events, want 2", len(replayed))
	}
	if replayed[0].ID != 4 || replayed[1].ID != 5 {
		t.Errorf("ids = %d, %d, want 4, 5", replayed[0].ID, replayed[1].ID)
	}
}

func TestBrokerUnsubscribeClosesChannel(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	stream := broker.Subscribe(ctx)
	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after cancel")
	}
}

// TestNewBrokerAtContinuesADurableRange pins the contract the SSE restart fix
// depends on: the first published event gets exactly the reserved id, and later
// events keep increasing from there.
func TestNewBrokerAtContinuesADurableRange(t *testing.T) {
	const start = 4097
	broker := NewBrokerAt(start, 8)

	var seen []uint64
	stream := broker.Subscribe(context.Background())
	for i := 0; i < 3; i++ {
		_ = broker.Publish("task.updated", map[string]any{"i": i})
	}
	for i := 0; i < 3; i++ {
		select {
		case event := <-stream:
			seen = append(seen, event.ID)
		case <-time.After(time.Second):
			t.Fatalf("event %d did not arrive", i)
		}
	}
	if seen[0] != start || seen[1] != start+1 || seen[2] != start+2 {
		t.Fatalf("ids = %v, want %d..%d", seen, start, start+2)
	}
	if last := broker.LastID(); last != start+2 {
		t.Fatalf("LastID = %d, want %d", last, start+2)
	}
	// A zero start is a programming mistake, not a reason to reuse id zero.
	zero := NewBrokerAt(0, 4)
	_ = zero.Publish("x", nil)
	if zero.LastID() != 1 {
		t.Fatalf("a zero start produced id %d, want 1", zero.LastID())
	}
}
