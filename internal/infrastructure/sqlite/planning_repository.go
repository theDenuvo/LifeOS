package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/application/settings"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domainplanning "github.com/theDenuvo/LifeOS/internal/domain/planning"
)

// PlanningUnitOfWork implements planning.UnitOfWork on top of SQLite.
//
// Planning touches three aggregates at once — the task (its planned date), the
// work blocks, and the calendar — so they share one transaction: a plan is either
// written completely or not at all.
type PlanningUnitOfWork struct {
	db *sql.DB
}

// NewPlanningUnitOfWork builds a unit of work over an open SQLite database.
func NewPlanningUnitOfWork(db *sql.DB) *PlanningUnitOfWork {
	return &PlanningUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *PlanningUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx planning.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, planningTransaction{tx: tx})
	})
}

type planningTransaction struct {
	tx *sql.Tx
}

func (t planningTransaction) Tasks() planning.TaskRepository { return &taskRepository{tx: t.tx} }

func (t planningTransaction) Events() planning.EventRepository { return &eventRepository{tx: t.tx} }

func (t planningTransaction) Blocks() planning.BlockRepository { return &blockRepository{tx: t.tx} }

func (t planningTransaction) Reminders() planning.ReminderRepository {
	return &reminderRepository{tx: t.tx}
}

func (t planningTransaction) Settings() planning.SettingsReader { return &settingsRepository{tx: t.tx} }

// Compile-time checks that the repositories satisfy the application ports.
var (
	_ tasks.TaskRepository        = (*taskRepository)(nil)
	_ events.Repository           = (*eventRepository)(nil)
	_ settings.Repository         = (*settingsRepository)(nil)
	_ assistant.Repository        = (*assistantRepository)(nil)
	_ planning.BlockRepository    = (*blockRepository)(nil)
	_ planning.ReminderRepository = (*reminderRepository)(nil)
)

// blockRepository persists planned work blocks.
type blockRepository struct {
	tx *sql.Tx
}

const blockColumns = `id, task_id, starts_at, ends_at, status, source, created_at, updated_at`

// ListRange returns blocks overlapping [from, to).
func (r *blockRepository) ListRange(ctx context.Context, from, to time.Time) ([]domainplanning.Block, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+blockColumns+` FROM task_blocks
		WHERE starts_at < ? AND ends_at > ?
		ORDER BY starts_at`,
		unixSeconds(to), unixSeconds(from))
	if err != nil {
		return nil, fmt.Errorf("list task blocks: %w", err)
	}
	defer rows.Close()

	found := make([]domainplanning.Block, 0, 16)
	for rows.Next() {
		value, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list task blocks: %w", err)
	}
	return found, nil
}

// Insert stores one block and returns it with its generated ID.
func (r *blockRepository) Insert(ctx context.Context, value domainplanning.Block) (domainplanning.Block, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO task_blocks (task_id, starts_at, ends_at, status, source, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)`,
		value.TaskID,
		unixSeconds(value.StartsAt),
		unixSeconds(value.EndsAt),
		string(value.Status),
		string(value.Source),
		unixSeconds(value.CreatedAt),
		unixSeconds(value.UpdatedAt),
	)
	if err != nil {
		return domainplanning.Block{}, fmt.Errorf("insert task block: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domainplanning.Block{}, fmt.Errorf("insert task block: read id: %w", err)
	}
	value.ID = id
	return value, nil
}

// Delete removes one block.
func (r *blockRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM task_blocks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task block %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task block %d: %w", id, err)
	}
	if affected == 0 {
		return ErrBlockNotFound
	}
	return nil
}

// ErrBlockNotFound is returned when a block no longer exists.
var ErrBlockNotFound = errors.New("task block not found")

func scanBlock(row rowScanner) (domainplanning.Block, error) {
	var (
		id        int64
		taskID    int64
		startsAt  int64
		endsAt    int64
		status    string
		source    string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&id, &taskID, &startsAt, &endsAt, &status, &source, &createdAt, &updatedAt); err != nil {
		return domainplanning.Block{}, fmt.Errorf("scan task block: %w", err)
	}
	value := domainplanning.Block{
		ID:        id,
		TaskID:    taskID,
		StartsAt:  timeFromUnix(startsAt),
		EndsAt:    timeFromUnix(endsAt),
		Status:    domainplanning.BlockStatus(status),
		Source:    domainplanning.BlockSource(source),
		CreatedAt: timeFromUnix(createdAt),
		UpdatedAt: timeFromUnix(updatedAt),
	}
	if !value.Status.IsValid() || !value.Source.IsValid() {
		return domainplanning.Block{}, fmt.Errorf("task block %d: invalid stored row", id)
	}
	return value, nil
}

// reminderRepository persists reminder moments.
type reminderRepository struct {
	tx *sql.Tx
}

const reminderColumns = `id, task_id, fire_at, kind, status, sent_at, notified_at, created_at`

// ReplaceForTask re-derives the pending reminders of a task.
//
// Delivered reminders are kept: they are part of what the user was told. Pending
// ones are replaced, because the deadline, priority or estimate they came from may
// have changed.
func (r *reminderRepository) ReplaceForTask(ctx context.Context, taskID int64, values []domainplanning.Reminder) error {
	if _, err := r.tx.ExecContext(ctx, `
		DELETE FROM task_reminders WHERE task_id = ? AND status = 'pending'`, taskID); err != nil {
		return fmt.Errorf("replace reminders: clear: %w", err)
	}
	for _, value := range values {
		if value.FireAt.IsZero() {
			continue
		}
		createdAt := value.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		_, err := r.tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO task_reminders (task_id, fire_at, kind, status, created_at)
			VALUES (?, ?, ?, 'pending', ?)`,
			taskID, unixSeconds(value.FireAt), string(value.Kind), unixSeconds(createdAt))
		if err != nil {
			return fmt.Errorf("replace reminders: insert: %w", err)
		}
	}
	return nil
}

// DismissForTask cancels the pending reminders of a task.
func (r *reminderRepository) DismissForTask(ctx context.Context, taskID int64) error {
	if _, err := r.tx.ExecContext(ctx, `
		DELETE FROM task_reminders WHERE task_id = ? AND status = 'pending'`, taskID); err != nil {
		return fmt.Errorf("dismiss reminders: %w", err)
	}
	return nil
}

// ListForTask returns every reminder of a task, oldest first.
func (r *reminderRepository) ListForTask(ctx context.Context, taskID int64) ([]domainplanning.Reminder, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+reminderColumns+` FROM task_reminders
		WHERE task_id = ? ORDER BY fire_at`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

// ListPending returns due reminders that have not been published yet.
//
// notified_at makes publishing happen once: a reminder that nobody listened to is
// not re-announced on every tick, it stays pending until a client picks it up.
func (r *reminderRepository) ListPending(ctx context.Context, until time.Time) ([]domainplanning.Reminder, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+reminderColumns+` FROM task_reminders
		WHERE status = 'pending' AND notified_at IS NULL AND fire_at <= ?
		ORDER BY fire_at LIMIT 100`, unixSeconds(until))
	if err != nil {
		return nil, fmt.Errorf("list pending reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

// ListActionable returns every due reminder the client was not told about.
//
// This is the durable side of delivery: it does not depend on a live connection,
// so a reminder that fired while the browser was closed is still offered when it
// comes back.
func (r *reminderRepository) ListActionable(ctx context.Context, until time.Time) ([]domainplanning.Reminder, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+reminderColumns+` FROM task_reminders
		WHERE status = 'pending' AND fire_at <= ?
		ORDER BY fire_at LIMIT 100`, unixSeconds(until))
	if err != nil {
		return nil, fmt.Errorf("list actionable reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

// MarkNotified records that the live event was published once.
func (r *reminderRepository) MarkNotified(ctx context.Context, id int64, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		UPDATE task_reminders SET notified_at = ?
		WHERE id = ? AND notified_at IS NULL`, unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("mark reminder %d notified: %w", id, err)
	}
	return nil
}

// MarkDelivered records that the user was actually shown the reminder.
func (r *reminderRepository) MarkDelivered(ctx context.Context, id int64, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		UPDATE task_reminders SET status = 'sent', sent_at = ?
		WHERE id = ? AND status = 'pending'`, unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("mark reminder %d delivered: %w", id, err)
	}
	return nil
}

func scanReminders(rows *sql.Rows) ([]domainplanning.Reminder, error) {
	found := make([]domainplanning.Reminder, 0, 8)
	for rows.Next() {
		var (
			id         int64
			taskID     int64
			fireAt     int64
			kind       string
			status     string
			sentAt     sql.NullInt64
			notifiedAt sql.NullInt64
			createdAt  int64
		)
		if err := rows.Scan(&id, &taskID, &fireAt, &kind, &status, &sentAt, &notifiedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan reminder: %w", err)
		}
		found = append(found, domainplanning.Reminder{
			ID:         id,
			TaskID:     taskID,
			FireAt:     timeFromUnix(fireAt),
			Kind:       domainplanning.ReminderKind(kind),
			Status:     domainplanning.ReminderStatus(status),
			SentAt:     timePtr(sentAt),
			NotifiedAt: timePtr(notifiedAt),
			CreatedAt:  timeFromUnix(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan reminders: %w", err)
	}
	return found, nil
}
