package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
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

// maxSkipJournalQueue bounds the asynchronous journal of refused launches.
// Recording a skip touches the database, and the receive loop must never
// wait for a database write — otherwise a burst during an overflow would
// stall the receiver and silently drop events in the broker buffer. The
// journal drains on its own; if even it saturates, the drop is logged
// loudly instead of staying silent.
const maxSkipJournalQueue = 64

// maxSkipWriteTimeout bounds a single refused-launch journal write, so a
// hung database operation cannot hold the drain — and with it the shutdown
// of the whole application — open forever. The drain always makes bounded
// progress: either the write lands or the deadline fails it loudly.
const maxSkipWriteTimeout = 5 * time.Second

// maxWorkerGrace is how long an in-flight scenario may keep working after
// shutdown began. It bounds the work itself, not the wait: when it elapses the
// runner cancels the work context, so tools and journal writes observe the
// cancellation and return, and the runner then waits for the real zero count.
// That distinction is what makes the shutdown guarantee absolute — a bounded
// wait alone would let the runner return with goroutines still writing.
const maxWorkerGrace = 10 * time.Second

// Shutdown runs three bounded stages, in this order: in-flight scenarios get
// maxWorkerGrace (10s) to finish and are cancelled when it elapses; each run's
// journal write, detached from cancellation, gets maxFinalizeTimeout (5s) —
// twice if the primary write fails and the fallback runs; the refusal journal
// gets maxDrainTimeout (5s) plus one in-flight write of maxSkipWriteTimeout
// (5s). The worst case is 30 seconds, which is what the wait in main is sized
// against. Every stage bounds the work, never merely the wait for it, so the
// runner always reaches a real zero worker count.

// maxDrainTimeout bounds the whole refusal-journal drain after shutdown:
// once it elapses, remaining writes fail fast on a cancelled context
// instead of finishing one by one. Together with one in-flight write the
// worst case is maxDrainTimeout + maxSkipWriteTimeout — the bound the
// application shutdown wait in main is sized against.
const maxDrainTimeout = 5 * time.Second

// maxFinishedWindows is how many finished run windows the attribution memory
// keeps. Runs are the only things that can cause deeper chains, and they
// finish within minutes at most, so a small ring covers the recent history.
// Active windows are never evicted — see remember.
const maxFinishedWindows = 32

// runWindow is one executing or executed run the runner remembers. Event
// attribution needs it: an event published while a run executes may be caused
// by that run, and a run started from it is one hop deeper in the chain.
type runWindow struct {
	start  time.Time
	finish time.Time // zero while the run is still executing
	depth  int
}

// skipRequest is one refused launch waiting to be journalled.
type skipRequest struct {
	automationID int64
	trigger      string
	reason       string
	at           time.Time
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
//
// Startup recovery of stale reservations is deliberately not done here: it
// belongs to main before the HTTP server opens, so recovery can never race a
// legitimate run that started after the server began accepting requests.
type Runner struct {
	service  *Service
	source   EventSource
	interval time.Duration

	mu       sync.Mutex
	running  []runWindow // active runs: never evicted while unfinished
	finished []runWindow // capped ring of completed runs

	// workerGrace is how long in-flight work may keep running after shutdown
	// before its context is cancelled; tests shorten it.
	workerGrace time.Duration

	// workers tracks every background goroutine the runner starts — event
	// runs and the cache refresher — so shutdown can wait for all of them
	// before the database closes.
	workers sync.WaitGroup

	cache eventCache
}

// RunnerOption adjusts a Runner at construction.
type RunnerOption func(*Runner)

// WithWorkerGrace overrides how long in-flight work may keep running after
// shutdown before its context is cancelled. The default is maxWorkerGrace.
func WithWorkerGrace(grace time.Duration) RunnerOption {
	return func(r *Runner) {
		if grace > 0 {
			r.workerGrace = grace
		}
	}
}

// NewRunner builds a runner. A zero interval defaults to 30 seconds, which is
// tight enough for minute-level schedules without hammering the database.
func NewRunner(service *Service, source EventSource, interval time.Duration, options ...RunnerOption) *Runner {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	runner := &Runner{service: service, source: source, interval: interval, workerGrace: maxWorkerGrace}
	for _, option := range options {
		option(runner)
	}
	return runner
}

// Start runs the loops until ctx is cancelled.
//
// It returns only when both loops have fully stopped: the tick loop ends
// with the context, and the event receiver finishes its orderly shutdown,
// including the refusal-journal drain. A caller that closes the database
// after Start returns cannot cut the drain short.
func (r *Runner) Start(ctx context.Context) {
	// Work runs on its own cancellation branch, detached from the parent's
	// cancellation: that is what gives in-flight scenarios a real grace
	// period instead of killing them the instant shutdown begins. The grace
	// period bounds the work — when it elapses cancelWork stops it — and the
	// wait for the workers themselves is absolute.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()

	receiverDone := make(chan struct{})
	go func() {
		defer close(receiverDone)
		r.watchEvents(ctx, workCtx, cancelWork)
	}()
	r.tickLoop(ctx)
	<-receiverDone
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
// automations. The receive loop stays cheap — matching runs against an
// in-memory cache, never against the database — so a burst cannot outrun it
// and overflow the broker's subscriber buffer; runs execute in bounded
// background work.
func (r *Runner) watchEvents(ctx context.Context, workCtx context.Context, cancelWork context.CancelFunc) {
	if r.source == nil {
		return
	}
	r.refreshCache(ctx)
	// The cache is the belt: automation events and a slow timer both refresh
	// it, so a missed automation.* event cannot leave stale subscriptions for
	// longer than one tick.
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		r.refreshLoop(ctx)
	}()

	queue := make(chan struct{}, maxDispatchQueue)
	// Refused launches are journalled off the receive path: the receiver
	// never waits for a database write, so a burst during an overflow keeps
	// flowing instead of stalling and dropping events in the broker buffer.
	skips := make(chan skipRequest, maxSkipJournalQueue)
	journalDone := make(chan struct{})
	// The drain context is cancelled maxDrainTimeout after the receive loop
	// has stopped: from then on the remaining queued refusals fail fast
	// instead of finishing one by one, so the whole drain is bounded by
	// maxDrainTimeout plus one in-flight write.
	drainCtx, drainCancel := context.WithCancel(context.WithoutCancel(ctx))
	go r.journalSkips(drainCtx, skips, journalDone)
	// Structured shutdown, in order: nothing new is received, the in-flight
	// event runs are allowed to finish (each with its journal write), the
	// refusal queue is closed and drained, and finally the remaining
	// background goroutines are joined. Nothing the runner started may still
	// touch the database after it returns.
	defer func() {
		r.stopWorkers(cancelWork)
		close(skips)
		timer := time.AfterFunc(maxDrainTimeout, drainCancel)
		defer timer.Stop()
		<-journalDone
		drainCancel()
		r.workers.Wait()
	}()

	// The loop exits on either the source closing its channel (as the broker
	// does when the context is cancelled) or the cancellation itself, so a
	// source that does not close still terminates the receiver.
	sub := r.source.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-sub:
			if !open {
				return
			}
			r.handleEvent(workCtx, queue, skips, event)
		}
	}
}

// handleEvent matches one incoming event against the cached subscriptions
// and dispatches the matched runs.
func (r *Runner) handleEvent(workCtx context.Context, queue chan struct{}, skips chan<- skipRequest, event events.Event) {
	if isAutomationEvent(event.Type) {
		r.refreshCache(workCtx)
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
	for _, id := range r.cache.match(event.Type, payload) {
		depth := r.depthFor(event.At)
		select {
		case queue <- struct{}{}:
			r.workers.Add(1)
			go func() {
				defer r.workers.Done()
				r.runEvent(workCtx, queue, id, depth, event.Type)
			}()
		default:
			// The queue is full: the run is refused before any step, and the
			// journal explains why instead of the event vanishing.
			r.enqueueSkip(skips, id, "очередь запусков переполнена, событие не обработано")
			slog.Warn("automation: dispatch queue full", "automation_id", id, "event", event.Type)
		}
	}
}

// enqueueSkip hands a refused launch to the asynchronous journal. The send
// never blocks the receiver; if the journal itself is saturated the refusal
// is logged loudly — an observable loss, not a silent one.
func (r *Runner) enqueueSkip(skips chan<- skipRequest, automationID int64, reason string) {
	request := skipRequest{automationID: automationID, trigger: "event", reason: reason, at: time.Now().UTC()}
	select {
	case skips <- request:
	default:
		slog.Warn("automation: skip journal saturated, refusal not recorded",
			"automation_id", automationID, "reason", reason)
	}
}

// journalSkips drains the refused-launch queue into the journal, one write
// at a time, away from the receive loop. It reads until the queue is closed
// and empty, so shutting the runner down flushes every refusal that was
// already queued; the writes run on a context detached from cancellation,
// because shutdown cancels the runner context while the queue may still
// hold work.
func (r *Runner) journalSkips(ctx context.Context, skips <-chan skipRequest, done chan<- struct{}) {
	defer close(done)
	for request := range skips {
		// Every write carries its own deadline and lives under the drain
		// context: a stuck write fails after the timeout, and once the drain
		// bound elapses the remaining writes fail immediately instead of
		// holding shutdown open indefinitely.
		writeCtx, cancel := context.WithTimeout(ctx, maxSkipWriteTimeout)
		r.service.recordSkipped(writeCtx, request.automationID, request.trigger, request.reason, request.at)
		cancel()
	}
}

// isAutomationEvent reports whether the event type changes the set of active
// event subscriptions and therefore requires a cache refresh.
func isAutomationEvent(eventType string) bool {
	switch eventType {
	case "automation.created", "automation.updated", "automation.deleted":
		return true
	}
	return false
}

// refreshLoop refreshes the subscription cache on a slow cadence as the
// braces to the event-driven invalidation.
func (r *Runner) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refreshCache(ctx)
		}
	}
}

func (r *Runner) refreshCache(ctx context.Context) {
	found, err := r.service.eventAutomations(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("automation: subscription cache refresh failed", "error", err)
		}
		return
	}
	r.cache.replace(found)
}

// runEvent executes one event-triggered run and remembers its execution
// window for chain attribution.
func (r *Runner) runEvent(ctx context.Context, queue <-chan struct{}, automationID int64, depth int, eventType string) {
	defer func() { <-queue }()
	window := runWindow{start: time.Now().UTC(), depth: depth}
	r.remember(window)
	if err := r.service.RunFromEvent(ctx, automationID, depth, window.start); err != nil {
		if ctx.Err() == nil {
			slog.Error("automation: event run failed to start", "id", automationID, "event", eventType, "error", err)
		}
	}
	r.closeWindow(window)
}

// grace reports the shutdown grace period for in-flight work.
func (r *Runner) grace() time.Duration {
	if r.workerGrace > 0 {
		return r.workerGrace
	}
	return maxWorkerGrace
}

// stopWorkers ends all in-flight work and waits until every background
// goroutine has actually returned.
//
// The grace period bounds the work, not the wait: a scenario gets
// maxWorkerGrace to finish on its own, and when that elapses the work context
// is cancelled so tools and journal writes observe it and return. Only then
// does the runner wait — unconditionally — for the real zero count, so it can
// never return while a worker is still touching the database.
func (r *Runner) stopWorkers(cancelWork context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		r.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(r.grace()):
	}
	slog.Warn("automation: cancelling work that outlived the shutdown grace period", "grace", r.grace())
	cancelWork()
	<-done
}

// remember records a run window. An unfinished window is never evicted: a
// long scenario must keep its attribution even if many short runs pass by,
// otherwise its later events would look like direct human actions.
func (r *Runner) remember(window runWindow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if window.finish.IsZero() {
		r.running = append(r.running, window)
		return
	}
	r.appendFinished(window)
}

func (r *Runner) appendFinished(window runWindow) {
	r.finished = append(r.finished, window)
	if len(r.finished) > maxFinishedWindows {
		r.finished = r.finished[len(r.finished)-maxFinishedWindows:]
	}
}

// closeWindow stamps the recorded window as finished and moves it into the
// evictable ring.
func (r *Runner) closeWindow(window runWindow) {
	finish := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.running {
		if r.running[i].start.Equal(window.start) && r.running[i].depth == window.depth {
			closed := r.running[i]
			closed.finish = finish
			r.running = append(r.running[:i], r.running[i+1:]...)
			r.appendFinished(closed)
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
	for _, window := range r.running {
		// A running window covers everything from its start onwards: the
		// finish moment is not known yet.
		if !at.Before(window.start) && window.depth+1 > depth {
			depth = window.depth + 1
		}
	}
	for _, window := range r.finished {
		if !at.Before(window.start) && !at.After(window.finish) && window.depth+1 > depth {
			depth = window.depth + 1
		}
	}
	return depth
}

// eventSub is one cached event subscription.
type eventSub struct {
	id      int64
	trigger domainautomation.EventTrigger
}

// eventCache keeps the active event-triggered automations in memory, so the
// receive loop matches without a database query per event. It is replaced
// atomically by refreshCache.
type eventCache struct {
	mu     sync.RWMutex
	subs   map[string][]eventSub
	loaded bool
}

func (c *eventCache) replace(automations []*domainautomation.Automation) {
	subs := map[string][]eventSub{}
	for _, item := range automations {
		if !item.Active() || item.Trigger.Kind != domainautomation.TriggerEvent || item.Trigger.Event == nil {
			continue
		}
		// Belt and braces to the trigger validation: run-lifecycle events can
		// never fire an automation, even if a row was written by hand.
		if domainautomation.IsReservedRunEvent(item.Trigger.Event.Name) {
			continue
		}
		subs[item.Trigger.Event.Name] = append(subs[item.Trigger.Event.Name], eventSub{id: item.ID, trigger: *item.Trigger.Event})
	}
	c.mu.Lock()
	c.subs = subs
	c.loaded = true
	c.mu.Unlock()
}

// match returns the automation ids the event should run. An unloaded cache
// matches nothing: the receiver loads it before processing events.
func (c *eventCache) match(eventType string, payload map[string]any) []int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return nil
	}
	var ids []int64
	for _, sub := range c.subs[eventType] {
		if sub.trigger.MatchesPayload(payload) {
			ids = append(ids, sub.id)
		}
	}
	return ids
}
