// Package planning implements the time-management use cases of LifeOS.
//
// Application orchestrates, Domain owns the rules, Infrastructure persists: the
// scheduling arithmetic lives in schedule.go, while the ports below are narrow
// interfaces that Infrastructure satisfies.
package planning

import (
	"context"
	"encoding/json"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
	"github.com/theDenuvo/LifeOS/internal/domain/planning"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Clock supplies the current instant. The planner never reads the wall clock
// itself, so a plan is reproducible in tests and always agrees with the deadline
// evaluation used elsewhere.
type Clock interface {
	Now() time.Time
}

// TaskRepository is the slice of the task store the planner needs.
type TaskRepository interface {
	List(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error)
	FindByID(ctx context.Context, id int64) (tasks.TaskWithVersion, error)
	Update(ctx context.Context, id int64, expectedVersion int, t *task.Task) (tasks.TaskWithVersion, error)
}

// EventRepository reads calendar events for the planning window.
type EventRepository interface {
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]events.EventWithVersion, error)
}

// BlockRepository persists planned work blocks.
//
// DeleteAutoRange removes only automatically planned blocks: the user's own
// blocks are never touched by the planner.
type BlockRepository interface {
	ListRange(ctx context.Context, from, to time.Time) ([]planning.Block, error)
	Insert(ctx context.Context, value planning.Block) (planning.Block, error)
	DeleteAutoRange(ctx context.Context, from, to time.Time) (int, error)
	Delete(ctx context.Context, id int64) error
}

// ReminderRepository persists reminder moments.
//
// ReplaceForTask is idempotent: it drops the pending reminders of the task and
// stores the freshly derived ones, keeping already delivered ones for the audit
// trail.
type ReminderRepository interface {
	ReplaceForTask(ctx context.Context, taskID int64, values []planning.Reminder) error
	DismissForTask(ctx context.Context, taskID int64) error
	ListForTask(ctx context.Context, taskID int64) ([]planning.Reminder, error)
	ListPending(ctx context.Context, until time.Time) ([]planning.Reminder, error)
	MarkSent(ctx context.Context, id int64, at time.Time) error
}

// SettingsReader reads user preferences. Missing keys fall back to defaults, so
// a fresh installation plans a normal working day without configuration.
type SettingsReader interface {
	Get(ctx context.Context, key string) (json.RawMessage, bool, error)
}

// Transaction exposes the repositories bound to one transaction.
type Transaction interface {
	Tasks() TaskRepository
	Events() EventRepository
	Blocks() BlockRepository
	Reminders() ReminderRepository
	Settings() SettingsReader
}

// UnitOfWork defines the transaction boundary for plan application.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Publisher publishes change events after a commit.
type Publisher interface {
	Publish(eventType string, data any)
}

// TaskReminderSync is implemented by Service and consumed by the task engine: a
// task change re-derives its reminders in the same breath, so the two never
// disagree.
type TaskReminderSync interface {
	SyncTaskReminders(ctx context.Context, value task.Task) error
}
