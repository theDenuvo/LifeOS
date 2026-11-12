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
	broker.Publish("task.created", map[string]any{"id": 1})

	event := receive(t, stream)
	if event.Type != "task.created" || event.ID != 1 {
		t.Fatalf("event = %+v", event)
	}
}

func TestBrokerReplayAfterID(t *testing.T) {
	broker := NewBroker()
	broker.Publish("task.created", map[string]any{"id": 1})
	broker.Publish("task.updated", map[string]any{"id": 1})
	broker.Publish("inbox.captured", map[string]any{"id": 2})

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
		broker.Publish("task.updated", map[string]any{"id": i})
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
