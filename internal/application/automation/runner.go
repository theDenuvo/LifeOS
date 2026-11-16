package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// EventSource is the slice of the broker the runner subscribes to.
type EventSource interface {
	Subscribe(ctx context.Context) <-chan events.Event
}

// Runner executes approved automations: schedule moments on a fixed cadence,
// LifeOS events as they arrive and condition insights on every pass.
type Runner struct {
	service  *Service
	source   EventSource
	interval time.Duration
}

// NewRunner builds a runner. A zero interval defaults to 30 seconds, which is
// tight enough for minute-level schedules without hammering the database.
func NewRunner(service *Service, source EventSource, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Runner{service: service, source: source, interval: interval}
}

// Start runs both loops until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
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
// automations. The channel is best effort: a slow run drops further events of
// the same burst, and schedule moments still fire on the tick.
func (r *Runner) watchEvents(ctx context.Context) {
	if r.source == nil {
		return
	}
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
		r.service.Observe(ctx, event.Type, payload, time.Now().UTC())
	}
}
