package recurrence

import (
	"context"
	"log/slog"
	"time"
)

// Store finds the recurring work that still needs its next occurrence.
//
// The state is derived from the tasks themselves instead of a queue table: a
// completed recurring task without a child is exactly the work that is missing. It
// therefore survives a restart, a crash between the completion and the creation,
// and a duplicate completion event, without any reconciliation of its own.
type Store interface {
	// PendingParents returns completed recurring tasks that have no next
	// occurrence yet, newest first.
	PendingParents(ctx context.Context, limit int) ([]int64, error)
	// ChildExists reports whether the next occurrence of the task exists.
	ChildExists(ctx context.Context, parentID int64) (bool, error)
}

// PendingOccurrence is one recurrence that could not be completed.
type PendingOccurrence struct {
	TaskID int64  `json:"task_id"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ReconcileResult reports one reconciliation pass.
type ReconcileResult struct {
	Checked int                 `json:"checked"`
	Created int                 `json:"created"`
	Pending []PendingOccurrence `json:"pending"`
}

// Pending lists the recurring tasks whose next occurrence is still missing.
//
// It is the user-visible side of a failed creation: the completed task stays
// completed, and the missing continuation is visible instead of silently lost.
func (s *Service) Pending(ctx context.Context, limit int) ([]PendingOccurrence, error) {
	if s.store == nil {
		return []PendingOccurrence{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	ids, err := s.store.PendingParents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PendingOccurrence, 0, len(ids))
	for _, id := range ids {
		found, err := s.tasks.GetTask(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, PendingOccurrence{TaskID: id, Title: found.Task.Title})
	}
	return out, nil
}

// Reconcile creates the missing next occurrences.
//
// It is safe to run at any time and as often as desired: Next skips a task whose
// child already exists, so a second pass after a partial failure creates nothing
// twice and completes what is still missing.
func (s *Service) Reconcile(ctx context.Context, limit int) (ReconcileResult, error) {
	result := ReconcileResult{Pending: []PendingOccurrence{}}
	if s.store == nil {
		return result, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	ids, err := s.store.PendingParents(ctx, limit)
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		result.Checked++
		completed, err := s.tasks.GetTask(ctx, id)
		if err != nil {
			result.Pending = append(result.Pending, PendingOccurrence{TaskID: id, Reason: "задача недоступна"})
			continue
		}
		if err := s.Next(ctx, completed); err != nil {
			result.Pending = append(result.Pending, PendingOccurrence{TaskID: id, Title: completed.Task.Title, Reason: err.Error()})
			continue
		}
		created, err := s.store.ChildExists(ctx, id)
		if err != nil {
			result.Pending = append(result.Pending, PendingOccurrence{TaskID: id, Title: completed.Task.Title, Reason: err.Error()})
			continue
		}
		if !created {
			// The rule ended or the occurrence is out of range: nothing is missing.
			continue
		}
		result.Created++
	}
	return result, nil
}

// Worker retries missing occurrences in the background.
type Worker struct {
	service  *Service
	interval time.Duration
}

// NewWorker builds a worker. A zero interval means "every minute".
func NewWorker(service *Service, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Worker{service: service, interval: interval}
}

// Run reconciles until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	// Run once immediately: a restart is exactly the case that needs recovery.
	w.tick(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	result, err := w.service.Reconcile(ctx, 25)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("reconcile recurring tasks failed", "error", err)
		}
		return
	}
	if result.Created > 0 {
		slog.Info("recurring tasks reconciled", "created", result.Created, "checked", result.Checked)
	}
	for _, item := range result.Pending {
		// A failure is retried on the next tick; logging it keeps it visible in the
		// meantime instead of silently losing the recurrence.
		slog.Warn("next occurrence still missing", "task_id", item.TaskID, "title", item.Title, "reason", item.Reason)
	}
}
