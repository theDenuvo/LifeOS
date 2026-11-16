package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// EventSource is the slice of the broker the runner subscribes to.
type EventSource interface {
	Subscribe(ctx context.Context) <-chan events.Event
}

// maxDispatchQueue bounds concurrent event-triggered runs. The reservation in
// StartRun already serialises one automation; this keeps a burst of events
// from spawning unbounded goroutines. An overflow is recorded as a skipped
// run, so refused work stays in the journal instead of disappearing silently.
const maxDispatchQueue = 8

// runWindow is one executing or executed run the runner remembers. Event
// attribution needs it: an event published while a run executes may be caused
// by that run, and a run started from it is one hop deeper in the chain.
type runWindow struct {
	start  time.Time
	finish time.Time // zero while the run is still executing
	depth  int
}

// Runner executes approved automations: schedule moments on a fixed cadence,
// LifeOS events as they arrive and condition insights on every pass.
//
// Event runs are dispatched asynchronously with a bounded queue, so a slow
// scenario never blocks the event receiver (the dispatch overflow is recorded
// as a skipped run), and every run carries a chain depth derived from the
// run whose execution published the triggering event. A run deeper than
// maxChainDepth is refused and journalled as skipped, which makes cycles —
// including an automation reacting to its own effects — impossible to
// sustain.
type Runner struct {
	service  *Service
	source   EventSource
	interval time.Duration

	mu      sync.Mutex
	windows []runWindow
}

// NewRunner builds a runner. A zero interval defaults to 30 seconds, which is
// tight enough for minute-level schedules without hammering the database.
func NewRunner(service *Service, source EventSource, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Runner{service: service, source: source, interval: interval}
}

// Start runs the loops until ctx is cancelled. The first thing it does is
// release the reservations a previous process may have left running.
func (r *Runner) Start(ctx context.Context) {
	if _, err := r.service.RecoverStaleRuns(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("automation: stale run recovery failed", "error", err)
	}
	go r.watchEvents(ctx)
	r.tickLoop(ctx)
}

// tickLoop recomputes and executes due work on a fixed cadence. The first
// pass runs shortly after startup, so moments missed while the process was
// down are caught up without waiting a full interval.
func (r *Runner) tickLoop(ctx context.Context) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.tick(ctx)
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	now := r.service.clock.Now().UTC()
	result := r.service.Tick(ctx, now)
	if result.ScheduleRuns+result.ConditionRuns > 0 {
		slog.Info("automation: runs executed",
			"schedule", result.ScheduleRuns, "condition", result.ConditionRuns,
			"catchups", result.OverdueCatchups, "busy_skipped", result.SkippedBusy)
	}
}

// watchEvents feeds every published LifeOS event to the event-triggered
// automations. The receive loop stays cheap (one matching query per event);
// runs execute in bounded background work so a long scenario cannot fill the
// broker's subscriber buffer and silently lose further events.
func (r *Runner) watchEvents(ctx context.Context) {
	if r.source == nil {
		return
	}
	queue := make(chan struct{}, maxDispatchQueue)
	defer close(queue)
	for event := range r.source.Subscribe(ctx) {
		if ctx.Err() != nil {
			return
		}
		payload, ok := event.Data.(map[string]any)
		if !ok && event.Data != nil {
			// A structured payload the automations could not interpret is not
			// worth panicking over; event triggers match on well-formed data.
			if data, err := json.Marshal(event.Data); err == nil {
				decoded := map[string]any{}
				if json.Unmarshal(data, &decoded) == nil {
					payload = decoded
				}
			}
		}
		if payload == nil {
			payload = map[string]any{}
		}
		matched, err := r.service.MatchedEvents(ctx, event.Type, payload)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("automation: event query failed", "event", event.Type, "error", err)
			}
			continue
		}
		for _, automation := range matched {
			depth := r.depthFor(event.At)
			select {
			case queue <- struct{}{}:
				go r.runEvent(ctx, queue, automation.ID, depth, event.Type)
			default:
				// The queue is full: the run is refused before any step, and
				// the journal explains why instead of the event vanishing.
				r.service.recordSkipped(ctx, automation.ID, "event",
					"очередь запусков переполнена, событие не обработано", time.Now().UTC())
				slog.Warn("automation: dispatch queue full", "automation_id", automation.ID, "event", event.Type)
			}
		}
	}
}

// runEvent executes one event-triggered run and remembers its execution
// window for chain attribution.
func (r *Runner) runEvent(ctx context.Context, queue <-chan struct{}, automationID int64, depth int, eventType string) {
	defer func() { <-queue }()
	window := runWindow{start: time.Now().UTC(), depth: int(depth)}
	r.remember(window)
	if err := r.service.RunFromEvent(ctx, automationID, int(depth), window.start); err != nil {
		if ctx.Err() == nil {
			slog.Error("automation: event run failed to start", "id", automationID, "event", eventType, "error", err)
		}
	}
	r.closeWindow(window)
}

// remember records a run window; the ring keeps the last few runs, which is
// all attribution needs because runs are the only things that can cause
// deeper chains and they finish within minutes at most.
func (r *Runner) remember(window runWindow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.windows = append(r.windows, window)
	if len(r.windows) > 32 {
		r.windows = r.windows[len(r.windows)-32:]
	}
}

// closeWindow stamps the recorded window as finished.
func (r *Runner) closeWindow(window runWindow) {
	finish := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.windows) - 1; i >= 0; i-- {
		if r.windows[i].start.Equal(window.start) && r.windows[i].depth == window.depth {
			r.windows[i].finish = finish
			return
		}
	}
}

// depthFor attributes an event published at `at` to the deepest run whose
// execution window contains that moment. An event with no covering window is
// a direct human (or system) action: depth zero. Events published while a
// run was executing are one hop deeper than that run; overlapping runs take
// the maximum, so a cycle cannot hide behind concurrency.
func (r *Runner) depthFor(at time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	depth := 0
	for _, window := range r.windows {
		end := window.finish
		if end.IsZero() {
			end = time.Now().UTC()
		}
		if !at.Before(window.start) && !at.After(end) {
			if window.depth+1 > depth {
				depth = window.depth + 1
			}
		}
	}
	return depth
}
