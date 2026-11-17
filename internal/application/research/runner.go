package research

import (
	"context"
	"log/slog"
	"time"
)

// Runner re-checks the standing watches whose moment has come.
//
// The cadence belongs to each watch (daily or weekly), so the runner only has
// to wake up often enough to notice: every fifteen minutes keeps a daily watch
// within a quarter of an hour of its moment without polling the network.
type Runner struct {
	service  *Service
	interval time.Duration
}

// NewRunner builds a watch runner. A zero or negative interval means "every
// fifteen minutes".
func NewRunner(service *Service, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &Runner{service: service, interval: interval}
}

// Run checks watches until the context is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if r.service == nil {
		return
	}
	// One pass at startup, so a watch whose moment passed while LifeOS was
	// stopped is checked as soon as the application is back.
	r.pass(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pass(ctx)
		}
	}
}

func (r *Runner) pass(ctx context.Context) {
	result := r.service.CheckDue(ctx, r.service.clock.Now().UTC())
	if result.Checked > 0 || result.Failed > 0 || result.Changed > 0 {
		slog.Info("research: watch pass", "checked", result.Checked, "changed", result.Changed, "failed", result.Failed)
	}
}
