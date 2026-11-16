// Package events provides the in-process event bus behind the SSE endpoint.
//
// It is deliberately small: LifeOS is a single-user modular monolith, so a
// process-local broker is enough. The broker keeps a bounded replay buffer so a
// reconnecting client can ask for everything after its Last-Event-ID instead of
// silently missing changes.
package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Event is one published change. Epoch identifies the durable id range the seq
// was handed out from, so a client can tell a stale position from a current one.
type Event struct {
	Epoch uint64
	ID    uint64
	Type  string
	Data  any
	// At is the wall-clock moment the event was published. The automation
	// runner uses it to attribute an event to the run whose execution
	// published it, which is what bounds self-triggering chains; other
	// consumers may ignore it.
	At time.Time
}

// Publisher is the write side used by application services. An error means the
// event was not published and must not be assumed delivered.
type Publisher interface {
	Publish(eventType string, data any) error
}

// IDReserve hands out the next block of event ids. The epoch identifies the
// range, start is the first seq of it; both come from durable storage.
type IDReserve func(ctx context.Context, block uint64) (epoch, start uint64, err error)

// Broker fans events out to subscribers and keeps a replay buffer.
type Broker struct {
	mu       sync.Mutex
	nextID   uint64
	epoch    uint64
	blockEnd uint64
	block    uint64
	reserve  IDReserve
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
	return NewBrokerAt(1, size)
}

// NewBrokerAt builds a broker whose first published event gets exactly start.
//
// A process may live for a while, so its ids have to be unique beyond its own
// lifetime: a browser reconnecting after a restart sends the last id it saw, and
// ids that begin again at one would all look older than that position. The
// caller passes the first id of a durably reserved range.
//
// The broker is unbounded: it never runs out of ids and never renews a block.
// Production builds use NewBrokerReserved, which keeps the handed-out ids inside
// the durably reserved range.
func NewBrokerAt(start uint64, size int) *Broker {
	if size < 1 {
		size = 1
	}
	if start == 0 {
		start = 1
	}
	return &Broker{
		nextID:   start - 1,
		ring:     make([]Event, 0, size),
		ringSize: size,
		subs:     make(map[int]chan Event),
	}
}

// reserveTimeout bounds one id reservation while a Publish waits for it.
const reserveTimeout = 3 * time.Second

// NewBrokerReserved builds a broker whose ids stay inside durable ranges.
//
// reserve is called once up front and again before the current range runs out,
// so Publish can never hand out an id the durable counter did not give it. The
// epoch returned by reserve is stamped on the events, which lets a reconnecting
// client prove its position belongs to this database instead of a restored
// older copy.
//
// An error here means the id ranges are not durable and the stream would
// eventually reuse ids; the caller must not start the application on it.
func NewBrokerReserved(size int, block uint64, reserve IDReserve) (*Broker, error) {
	broker := NewBrokerAt(0, size)
	if block == 0 {
		return nil, errors.New("event ids: block must be positive")
	}
	if reserve == nil {
		return nil, errors.New("event ids: reserve function is required")
	}
	broker.block = block
	broker.reserve = reserve
	if err := broker.renewBlock(); err != nil {
		return nil, err
	}
	return broker, nil
}

// renewBlock swaps in the next durable id range. It is called under the mutex.
func (b *Broker) renewBlock() error {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTimeout)
	defer cancel()
	epoch, start, err := b.reserve(ctx, b.block)
	if err != nil {
		return fmt.Errorf("reserve event ids: %w", err)
	}
	if epoch == 0 {
		return errors.New("reserve event ids: epoch must not be zero")
	}
	if start <= b.nextID {
		return fmt.Errorf("reserve event ids: range starts at %d, which overlaps ids already handed out (last %d)", start, b.nextID)
	}
	b.epoch = epoch
	b.nextID = start - 1
	// The block ends where the next reservation must begin.
	b.blockEnd = start + b.block - 1
	return nil
}

// Publish records an event and delivers it to every current subscriber.
//
// The whole operation happens under the mutex, and delivery is non-blocking:
// a subscriber that cannot keep up misses the event rather than stalling the
// writer, while SSE clients recover missed events by reconnecting with
// Last-Event-ID. Holding the lock across the non-blocking sends is what
// guarantees two things the stream relies on: events reach subscribers in
// ascending ID order, and a channel cancelled concurrently cannot be closed
// while a send is in flight (no "send on closed channel" panic).
func (b *Broker) Publish(eventType string, data any) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A reserved broker must never leave its durable range: when the next id
	// would be past the block, get the next block first. An error here refuses
	// the event instead of silently reusing an id a browser may still hold.
	if b.reserve != nil && b.nextID+1 > b.blockEnd {
		if err := b.renewBlock(); err != nil {
			return err
		}
	}
	b.nextID++
	event := Event{Epoch: b.epoch, ID: b.nextID, Type: eventType, Data: data, At: time.Now().UTC()}
	b.ring = append(b.ring, event)
	if len(b.ring) > b.ringSize {
		b.ring = b.ring[len(b.ring)-b.ringSize:]
	}
	for _, ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
	return nil
}

// Epoch reports the epoch of the current durable id range. A reconnecting
// client whose epoch differs is holding ids from another database state and
// must resynchronise instead of trusting the numbers.
func (b *Broker) Epoch() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.epoch
}

// Head returns the current epoch and the last handed-out id together.
//
// Reading them as one value under the mutex is what lets a reconnecting handler
// build a resync cursor that belongs to one epoch: two separate reads could
// straddle a block renewal, which changes both.
func (b *Broker) Head() (epoch, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.epoch, b.nextID
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
