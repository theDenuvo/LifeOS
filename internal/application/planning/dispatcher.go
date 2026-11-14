package planning

import (
	"context"
	"log/slog"
	"time"
)

// Dispatcher delivers due reminders to the interface.
//
// Reminders are computed when a task changes and only *delivered* here, so the
// schedule is always consistent with the deadline it belongs to. Delivery is a
// published event: the browser shows a notification without polling, and nothing
// is announced for a task that was completed in the meantime.
type Dispatcher struct {
	planner   *Service
	publisher Publisher
	clock     Clock
	interval  time.Duration
}

// NewDispatcher builds a dispatcher. A zero interval means "every 30 seconds",
// which is fast enough to feel immediate and cheap enough to ignore.
func NewDispatcher(planner *Service, publisher Publisher, clock Clock, interval time.Duration) *Dispatcher {
	if clock == nil {
		clock = systemClock{}
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Dispatcher{planner: planner, publisher: publisher, clock: clock, interval: interval}
}

// Run delivers reminders until the context is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("deliver reminders failed", "error", err)
			}
		}
	}
}

// Tick delivers every reminder that is due now.
//
// A reminder is marked sent only after it was published, so a failed delivery is
// retried on the next tick instead of being silently dropped.
func (d *Dispatcher) Tick(ctx context.Context) error {
	now := d.clock.Now()
	due, err := d.planner.DueReminders(ctx, now)
	if err != nil {
		return err
	}
	for _, item := range due {
		if item.Task == nil {
			continue
		}
		if d.publisher != nil {
			d.publisher.Publish("reminder", reminderPayload(item, now))
		}
		if err := d.planner.MarkReminderSent(ctx, item.Reminder.ID, now); err != nil {
			return err
		}
	}
	return nil
}

// reminderPayload is what the browser receives.
func reminderPayload(item ReminderView, now time.Time) map[string]any {
	value := item.Task
	payload := map[string]any{
		"reminder_id":  item.Reminder.ID,
		"task_id":      value.ID,
		"title":        value.Title,
		"kind":         string(item.Reminder.Kind),
		"priority":     string(value.Priority),
		"fire_at":      item.Reminder.FireAt.Format(time.RFC3339),
		"delivered_at": now.Format(time.RFC3339),
	}
	if value.Note != nil {
		payload["note"] = *value.Note
	}
	if value.EstimateMinutes != nil {
		payload["estimate_minutes"] = *value.EstimateMinutes
	}
	if value.DueAt != nil {
		payload["due_at"] = value.DueAt.Format(time.RFC3339)
	}
	if value.DueDate != nil && !value.DueDate.IsZero() {
		payload["due_date"] = value.DueDate.String()
	}
	payload["overdue"] = value.IsOverdue(now)
	if value.Repeat != nil {
		payload["repeat"] = value.Repeat.Describe()
	}
	return payload
}
