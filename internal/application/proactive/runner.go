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
//
// Roadmap stage 5: the runner is also where initiative happens. After the
// insights are computed, the outreach gate decides whether one of them is
// worth starting a dialogue about, and finished conversations are checked for
// replies so a reaction is recorded without a button press.
type Runner struct {
	service   *Service
	outreach  *OutreachService
	publisher events.Publisher
	interval  time.Duration
	seen      map[string]bool
}

// NewRunner builds a runner. A zero interval defaults to 15 minutes. The
// outreach service is optional: without it the runner only announces insights.
func NewRunner(service *Service, publisher events.Publisher, interval time.Duration, outreach ...*OutreachService) *Runner {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	r := &Runner{
		service:   service,
		publisher: publisher,
		interval:  interval,
		seen:      make(map[string]bool),
	}
	for _, item := range outreach {
		if item != nil {
			r.outreach = item
		}
	}
	return r
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
			payload := map[string]any{
				"key":      insight.Key,
				"title":    insight.Title,
				"severity": string(insight.Severity),
				"count":    insight.Count,
			}
			if err := r.publisher.Publish("insight.detected", payload); err != nil {
				// The insight is recorded durably; a lost live event only
				// delays when the interface shows it.
				slog.Error("publish insight failed", "key", insight.Key, "error", err)
			}
		}
	}
	r.seen = current

	// Initiative runs after the announcement, so the panel and the chat show
	// the same picture in the same pass. A reply check may finish a previous
	// outreach even when no new insight exists.
	if r.outreach != nil {
		r.outreach.Evaluate(ctx, insights)
		r.outreach.AcknowledgeReplies(ctx)
	}
}
