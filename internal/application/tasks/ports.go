// Package tasks implements the Task Engine application layer.
//
// The application layer orchestrates use cases: it coordinates domain
// operations and defines the interfaces (ports) that Infrastructure will later
// implement. It owns no business rules of its own — those live in
// internal/domain/task — and it knows nothing about SQLite, HTTP, LLMs or MCP.
//
// Guiding rule: Application orchestrates, Domain owns the rules, Infrastructure
// persists.
package tasks

import (
	"context"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Clock supplies the current instant. It exists so that use cases are
// reproducible in tests and so that one operation can stamp the task and its
// event with the very same timestamp.
//
// The domain never reads the clock itself; only this layer does.
type Clock interface {
	Now() time.Time
}

// systemClock is the production Clock backed by the system time.
type systemClock struct{}

// Now returns the current UTC instant.
func (systemClock) Now() time.Time { return time.Now().UTC() }

// SystemClock returns a Clock that reads the real system time.
func SystemClock() Clock { return systemClock{} }

// TaskRepository persists task aggregates. It is a port: Infrastructure
// implements it, the application only depends on it.
//
// Every write method carries the expected version and performs the update
// atomically (WHERE id = ? AND version = ?), so a stale writer cannot silently
// overwrite a newer state. Reads never change the version.
type TaskRepository interface {
	// FindByID returns the task with its concurrency metadata.
	// It returns ErrTaskNotFound when no such task exists.
	FindByID(ctx context.Context, id int64) (TaskWithVersion, error)

	// List returns a page of tasks matching the filter, plus the total number
	// of matching rows (ignoring limit/offset).
	List(ctx context.Context, filter TaskFilter) (ListTasksResult, error)

	// Insert stores a new task and returns the generated ID. The stored version
	// starts at 1.
	Insert(ctx context.Context, t *task.Task) (int64, error)

	// Update writes the task only if the stored version still equals
	// expectedVersion, incrementing the version by exactly one. The comparison
	// and the increment must happen in a single statement (WHERE id = ? AND
	// version = ?), so a stale writer can never overwrite a newer state.
	// It returns ErrTaskNotFound when the row is gone and ErrVersionConflict
	// when the version does not match.
	Update(ctx context.Context, id int64, expectedVersion int, t *task.Task) (TaskWithVersion, error)
}

// TaskEventRepository appends audit history. TaskEvent is an append-only audit
// log, not an event store: nothing is ever updated or replayed.
type TaskEventRepository interface {
	// Insert appends an event to the history.
	Insert(ctx context.Context, e *task.TaskEvent) error
}

// UnitOfWork defines a transaction boundary.
//
// The application opens a unit of work, obtains the repositories bound to that
// transaction, performs the domain operation, and commits or rolls back. Task
// and its TaskEvent must be written inside one unit of work: if the event
// insert fails, the whole operation is rolled back.
type UnitOfWork interface {
	// Do runs fn inside a transaction, committing when fn returns nil and
	// rolling back when it returns an error or the context is cancelled.
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Transaction exposes the repositories bound to a single transaction.
type Transaction interface {
	// Tasks returns the task repository bound to this transaction.
	Tasks() TaskRepository

	// Events returns the task event repository bound to this transaction.
	Events() TaskEventRepository
}
