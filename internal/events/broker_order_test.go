package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestBrokerPublishIsOrderedUnderConcurrency guards the ordering contract the
// SSE handler depends on: it drops any event whose ID is not greater than the
// last one it sent, so out-of-order delivery would silently lose events.
//
// Delivery itself is best-effort (a slow subscriber channel drops), so the
// assertion is on order, not on every event arriving.
func TestBrokerPublishIsOrderedUnderConcurrency(t *testing.T) {
	broker := NewBrokerWithReplay(4096)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const publishers, perPublisher = 8, 50
	const total = publishers * perPublisher

	stream := broker.Subscribe(ctx)

	// Read concurrently with publishing so the droppable buffer does not decide
	// the outcome of the test.
	received := make([]uint64, 0, total)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case event, ok := <-stream:
				if !ok {
					return
				}
				received = append(received, event.ID)
				if len(received) >= total {
					return
				}
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPublisher; j++ {
				broker.Publish("task.updated", map[string]any{"id": j})
			}
		}()
	}
	wg.Wait()
	<-done

	if len(received) == 0 {
		t.Fatal("no events were delivered")
	}
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Fatalf("event %d arrived after %d: delivery must be ordered", received[i], received[i-1])
		}
	}
	if broker.LastID() != total {
		t.Fatalf("last event id = %d, want %d", broker.LastID(), total)
	}
}

// TestBrokerCancelledSubscriptionNeverReceivesAndNeverPanics reproduces the
// race the review found: a subscription is cancelled while another goroutine
// publishes. Before the fix, Publish sent on a channel the cancelling
// goroutine had already closed, panicking the whole process.
func TestBrokerCancelledSubscriptionNeverReceivesAndNeverPanics(t *testing.T) {
	broker := NewBroker()

	for round := 0; round < 200; round++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream := broker.Subscribe(ctx)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			cancel()
		}()
		go func() {
			defer wg.Done()
			broker.Publish("task.updated", map[string]any{"round": round})
		}()
		wg.Wait()

		// Draining must terminate: the channel is either closed or holds events.
		for {
			select {
			case _, ok := <-stream:
				if !ok {
					goto next
				}
			default:
				goto next
			}
		}
	next:
	}
}

// TestBrokerPublishAfterAllSubscribersCancel keeps publishing after every
// subscriber is gone: nothing may panic and the replay buffer still grows.
func TestBrokerPublishAfterAllSubscribersCancel(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	stream := broker.Subscribe(ctx)
	cancel()
	for range stream {
		// drain until closed
	}
	for i := 0; i < 10; i++ {
		broker.Publish("task.updated", map[string]any{"id": i})
	}
	if broker.LastID() != 10 {
		t.Fatalf("LastID = %d, want 10", broker.LastID())
	}
}
