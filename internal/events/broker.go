// Package events provides the in-process event bus behind the SSE endpoint.
//
// It is deliberately small: LifeOS is a single-user modular monolith, so a
// process-local broker is enough. The broker keeps a bounded replay buffer so a
// reconnecting client can ask for everything after its Last-Event-ID instead of
// silently missing changes.
package events

import (
	"context"
	"sync"
)

// Event is one published change.
type Event struct {
	ID   uint64
	Type string
	Data any
}

// Publisher is the write side used by application services.
type Publisher interface {
	Publish(eventType string, data any)
}

// Broker fans events out to subscribers and keeps a replay buffer.
type Broker struct {
	mu       sync.Mutex
	nextID   uint64
	ring     []Event
	ringSize int
	subs     map[int]chan Event
	nextSub  int
}

// DefaultReplaySize is the number of events kept for Last-Event-ID replay.
const DefaultReplaySize = 256

// NewBroker builds a broker with the default replay buffer.
func NewBroker() *Broker {
	return NewBrokerWithReplay(DefaultReplaySize)
}

// NewBrokerWithReplay builds a broker with a custom replay buffer size.
func NewBrokerWithReplay(size int) *Broker {
	if size < 1 {
		size = 1
	}
	return &Broker{
		ring:     make([]Event, 0, size),
		ringSize: size,
		subs:     make(map[int]chan Event),
	}
}

// Publish records an event and delivers it to every current subscriber.
//
// Delivery is non-blocking: a subscriber that cannot keep up misses the event
// rather than stalling the writer. SSE clients recover missed events by
// reconnecting with Last-Event-ID.
func (b *Broker) Publish(eventType string, data any) {
	b.mu.Lock()
	b.nextID++
	event := Event{ID: b.nextID, Type: eventType, Data: data}
	b.ring = append(b.ring, event)
	if len(b.ring) > b.ringSize {
		b.ring = b.ring[len(b.ring)-b.ringSize:]
	}
	subs := make([]chan Event, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

// Subscribe registers a subscriber until ctx is cancelled.
//
// The returned channel is closed when the subscription ends, so a consumer can
// range over it safely.
func (b *Broker) Subscribe(ctx context.Context) <-chan Event {
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	ch := make(chan Event, 64)
	b.subs[id] = ch
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if current, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(current)
		}
		b.mu.Unlock()
	}()

	return ch
}

// Replay returns buffered events with an ID greater than afterID.
//
// An afterID of zero returns the whole buffer, which is what a fresh client
// needs only if it wants history; the SSE handler uses it to catch up a
// reconnecting client.
func (b *Broker) Replay(afterID uint64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	replayed := make([]Event, 0, len(b.ring))
	for _, event := range b.ring {
		if event.ID > afterID {
			replayed = append(replayed, event)
		}
	}
	return replayed
}

// LastID returns the most recently published event ID.
func (b *Broker) LastID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextID
}
