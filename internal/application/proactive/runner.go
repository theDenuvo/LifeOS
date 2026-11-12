package proactive

import (
	"context"
	"log/slog"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// Runner periodically recomputes insights and publishes newly appearing ones.
//
// It is deliberately quiet: an insight is announced once when it first appears,
// and again only after it disappeared and came back. Dismissed and snoozed
// insights are filtered by the service before the runner sees them.
type Runner struct {
	service   *Service
	publisher events.Publisher
	interval  time.Duration
	seen      map[string]bool
}

// NewRunner builds a runner. A zero interval defaults to 15 minutes.
func NewRunner(service *Service, publisher events.Publisher, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &Runner{
		service:   service,
		publisher: publisher,
		interval:  interval,
		seen:      make(map[string]bool),
	}
}

// Start runs the loop until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// First pass shortly after startup so the user sees current problems
	// without waiting a full interval.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.RunOnce(ctx)
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce computes insights and publishes the ones that are new since the last
// pass.
func (r *Runner) RunOnce(ctx context.Context) {
	insights, err := r.service.Compute(ctx)
	if err != nil {
		slog.Warn("proactive: compute failed", "error", err)
		return
	}

	current := make(map[string]bool, len(insights))
	for _, insight := range insights {
		current[insight.Key] = true
		if r.seen[insight.Key] {
			continue
		}
		if r.publisher != nil {
			r.publisher.Publish("insight.detected", map[string]any{
				"key":      insight.Key,
				"title":    insight.Title,
				"severity": string(insight.Severity),
				"count":    insight.Count,
			})
		}
	}
	r.seen = current
}
