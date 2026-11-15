package goals

import (
	"context"
	"log/slog"
	"time"
)

// Reconciler keeps the goal progress journal consistent with fact.
//
// The live pump in cmd/lifeos turns "task.completed" events into journal
// entries, but a single event is not a guarantee: LifeOS may be down when the
// task is completed, a slow subscriber may miss the event, or the journal write
// itself may fail. The reconciler walks the existing links on a schedule and
// records whatever is missing. Recording is idempotent, so a run over already
// consistent data writes nothing.
type Reconciler struct {
	service  *Service
	interval time.Duration
}

// DefaultReconcileInterval is how often the journal is re-checked. Completion
// events are the primary path; this is the safety net.
const DefaultReconcileInterval = time.Hour

// NewReconciler builds the periodic journal reconciler.
func NewReconciler(service *Service, interval time.Duration) *Reconciler {
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	return &Reconciler{service: service, interval: interval}
}

// Run reconciles immediately on start — this is what recovers entries missed
// while the process was down — and then on every tick until ctx is done.
func (r *Reconciler) Run(ctx context.Context) {
	if r == nil || r.service == nil {
		return
	}
	r.reconcile(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	added, err := r.service.ReconcileTaskProgress(ctx)
	if err != nil {
		if ctx.Err() == nil {
			// A failed reconciliation is retried on the next tick.
			slog.Warn("goal progress reconciliation failed", "error", err)
		}
		return
	}
	if added > 0 {
		slog.Info("goal progress reconciliation added journal entries", "entries", added)
	}
}
