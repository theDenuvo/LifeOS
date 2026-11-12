package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// TaskUnitOfWork implements tasks.UnitOfWork on top of SQLite.
//
// Every use case runs inside one transaction. The repositories handed to the
// callback are bound to that transaction, so a task and its audit event are
// written atomically: if the event insert fails, the task write and the version
// bump are rolled back with it.
type TaskUnitOfWork struct {
	db *sql.DB
}

// NewTaskUnitOfWork builds a unit of work over an open SQLite database.
func NewTaskUnitOfWork(db *sql.DB) *TaskUnitOfWork {
	return &TaskUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
//
// It commits only when fn returns nil and the context is still alive; any error
// or a cancelled context rolls the whole unit of work back.
func (u *TaskUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx tasks.Transaction) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(ctx, taskTransaction{tx: tx}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// taskTransaction exposes repositories bound to one transaction.
type taskTransaction struct {
	tx *sql.Tx
}

func (t taskTransaction) Tasks() tasks.TaskRepository { return &taskRepository{tx: t.tx} }

func (t taskTransaction) Events() tasks.TaskEventRepository { return &taskEventRepository{tx: t.tx} }

// taskRepository is the SQLite implementation of tasks.TaskRepository.
type taskRepository struct {
	tx *sql.Tx
}

const taskColumns = `id, title, note, priority, status, planned_date, due_date, due_at,
	timezone, tags, created_at, updated_at, completed_at, cancelled_at, parent_task_id, version`

// FindByID loads one task with its version.
func (r *taskRepository) FindByID(ctx context.Context, id int64) (tasks.TaskWithVersion, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	found, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return tasks.TaskWithVersion{}, tasks.ErrTaskNotFound
	}
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("find task %d: %w", id, err)
	}
	return found, nil
}

// Insert stores a new task and returns its generated ID.
func (r *taskRepository) Insert(ctx context.Context, t *task.Task) (int64, error) {
	if err := t.Validate(); err != nil {
		return 0, err
	}

	tags, err := encodeTags(t.Tags)
	if err != nil {
		return 0, err
	}

	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO tasks (
			title, note, priority, status, planned_date, due_date, due_at,
			timezone, tags, created_at, updated_at, completed_at, cancelled_at,
			parent_task_id, version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		t.Title,
		encodeStringPtr(t.Note),
		string(t.Priority),
		string(t.Status),
		encodeCalendarDate(t.PlannedDate),
		encodeCalendarDate(t.DueDate),
		encodeTimePtr(t.DueAt),
		t.Timezone,
		tags,
		unixSeconds(t.CreatedAt),
		unixSeconds(t.UpdatedAt),
		encodeTimePtr(t.CompletedAt),
		encodeTimePtr(t.CancelledAt),
		encodeParentID(t.ParentTaskID),
	)
	if err != nil {
		return 0, fmt.Errorf("insert task: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert task: read id: %w", err)
	}
	return id, nil
}

// Update performs the conditional write required by optimistic locking.
//
// The version comparison and the increment happen in the same statement, so two
// transactions that both read version N cannot both commit version N+1: the
// second one updates zero rows and is reported as a conflict.
func (r *taskRepository) Update(ctx context.Context, id int64, expectedVersion int, t *task.Task) (tasks.TaskWithVersion, error) {
	if err := t.Validate(); err != nil {
		return tasks.TaskWithVersion{}, err
	}

	tags, err := encodeTags(t.Tags)
	if err != nil {
		return tasks.TaskWithVersion{}, err
	}

	result, err := r.tx.ExecContext(ctx, `
		UPDATE tasks SET
			title = ?, note = ?, priority = ?, status = ?, planned_date = ?,
			due_date = ?, due_at = ?, timezone = ?, tags = ?, created_at = ?,
			updated_at = ?, completed_at = ?, cancelled_at = ?, parent_task_id = ?,
			version = version + 1
		WHERE id = ? AND version = ?`,
		t.Title,
		encodeStringPtr(t.Note),
		string(t.Priority),
		string(t.Status),
		encodeCalendarDate(t.PlannedDate),
		encodeCalendarDate(t.DueDate),
		encodeTimePtr(t.DueAt),
		t.Timezone,
		tags,
		unixSeconds(t.CreatedAt),
		unixSeconds(t.UpdatedAt),
		encodeTimePtr(t.CompletedAt),
		encodeTimePtr(t.CancelledAt),
		encodeParentID(t.ParentTaskID),
		id,
		expectedVersion,
	)
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("update task %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("update task %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		// Distinguish "row is gone" from "row moved on": both must not be
		// reported as a successful write, but the caller handles them
		// differently.
		if _, err := r.FindByID(ctx, id); errors.Is(err, tasks.ErrTaskNotFound) {
			return tasks.TaskWithVersion{}, tasks.ErrTaskNotFound
		}
		return tasks.TaskWithVersion{}, tasks.ErrVersionConflict
	}

	updated, err := r.FindByID(ctx, id)
	if err != nil {
		return tasks.TaskWithVersion{}, err
	}
	return updated, nil
}

// List returns a filtered page plus the total number of matching rows.
//
// Overdue is derived from each task's own timezone and deadline semantics
// (Task.IsOverdue), which SQLite cannot evaluate, so the overdue filter is
// applied in Go after a broad SQL prefilter. Every other filter is pushed into
// SQL and paginated there.
func (r *taskRepository) List(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error) {
	where, args := buildTaskWhere(filter)
	order := buildTaskOrder(filter.Sorts)

	limit := filter.Limit.Or(tasks.DefaultPageLimit)
	offset := filter.Offset.Or(0)

	if filter.Overdue.IsSet() {
		rows, err := r.tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks`+where+order, args...)
		if err != nil {
			return tasks.ListTasksResult{}, fmt.Errorf("list tasks: %w", err)
		}
		defer rows.Close()

		matches, err := scanTasks(rows)
		if err != nil {
			return tasks.ListTasksResult{}, err
		}

		asOf := filter.AsOf.Or(time.Time{})
		wantOverdue := filter.Overdue.Or(false)
		filtered := make([]tasks.TaskWithVersion, 0, len(matches))
		for _, item := range matches {
			if item.Task.IsOverdue(asOf) == wantOverdue {
				filtered = append(filtered, item)
			}
		}

		total := len(filtered)
		return tasks.ListTasksResult{
			Tasks:  page(filtered, limit, offset),
			Total:  total,
			Limit:  limit,
			Offset: offset,
		}, nil
	}

	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`+where, args...).Scan(&total); err != nil {
		return tasks.ListTasksResult{}, fmt.Errorf("count tasks: %w", err)
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks`+where+order+` LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return tasks.ListTasksResult{}, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	found, err := scanTasks(rows)
	if err != nil {
		return tasks.ListTasksResult{}, err
	}

	return tasks.ListTasksResult{Tasks: found, Total: total, Limit: limit, Offset: offset}, nil
}

// taskEventRepository appends audit events; it never updates or deletes.
type taskEventRepository struct {
	tx *sql.Tx
}

// Insert appends one event.
func (r *taskEventRepository) Insert(ctx context.Context, e *task.TaskEvent) error {
	if e == nil {
		return errors.New("insert task event: nil event")
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	_, err := r.tx.ExecContext(ctx, `
		INSERT INTO task_events (task_id, type, payload, actor, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		e.TaskID, string(e.Type), string(payload), string(e.Actor), unixSeconds(e.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert task event: %w", err)
	}
	return nil
}

// buildTaskWhere renders the SQL predicate for a filter.
func buildTaskWhere(filter tasks.TaskFilter) (string, []any) {
	var clauses []string
	var args []any

	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(status))
		}
		clauses = append(clauses, "status IN ("+strings.Join(placeholders, ",")+")")
	}

	if len(filter.Priorities) > 0 {
		placeholders := make([]string, len(filter.Priorities))
		for i, priority := range filter.Priorities {
			placeholders[i] = "?"
			args = append(args, string(priority))
		}
		clauses = append(clauses, "priority IN ("+strings.Join(placeholders, ",")+")")
	}

	if value, ok := filter.PlannedDate.Get(); ok {
		if value.IsZero() {
			clauses = append(clauses, "planned_date IS NULL")
		} else {
			clauses = append(clauses, "planned_date = ?")
			args = append(args, value.String())
		}
	}

	if value, ok := filter.DueDate.Get(); ok {
		if value.IsZero() {
			clauses = append(clauses, "due_date IS NULL")
		} else {
			clauses = append(clauses, "due_date = ?")
			args = append(args, value.String())
		}
	}

	if value, ok := filter.Search.Get(); ok {
		term := strings.TrimSpace(value)
		if term != "" {
			pattern := "%" + escapeLike(term) + "%"
			clauses = append(clauses, `(title LIKE ? ESCAPE '\' OR IFNULL(note, '') LIKE ? ESCAPE '\')`)
			args = append(args, pattern, pattern)
		}
	}

	for _, tag := range filter.Tags {
		clauses = append(clauses, "EXISTS (SELECT 1 FROM json_each(tasks.tags) WHERE json_each.value = ?)")
		args = append(args, tag)
	}

	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// buildTaskOrder renders a whitelisted ORDER BY clause.
//
// Unknown fields are rejected earlier by the application layer; this function
// only ever maps known constants to SQL, never user text.
func buildTaskOrder(sorts []tasks.TaskSort) string {
	if len(sorts) == 0 {
		return " ORDER BY created_at DESC, id DESC"
	}

	parts := make([]string, 0, len(sorts)+1)
	for _, sort := range sorts {
		column := ""
		switch sort.Field {
		case tasks.SortCreatedAt:
			column = "created_at"
		case tasks.SortUpdatedAt:
			column = "updated_at"
		case tasks.SortDueAt:
			column = "due_at"
		case tasks.SortPriority:
			column = "CASE priority WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END"
		case tasks.SortTitle:
			column = "title COLLATE NOCASE"
		default:
			column = "created_at"
		}
		direction := "ASC"
		if sort.Order == tasks.SortDesc {
			direction = "DESC"
		}
		parts = append(parts, column+" "+direction)
	}
	// Stable pagination regardless of duplicate sort values.
	parts = append(parts, "id ASC")
	return " ORDER BY " + strings.Join(parts, ", ")
}

// scanTasks drains a result set.
func scanTasks(rows *sql.Rows) ([]tasks.TaskWithVersion, error) {
	found := make([]tasks.TaskWithVersion, 0, 16)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan tasks: %w", err)
	}
	return found, nil
}

// rowScanner is implemented by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTask converts one row into the domain aggregate and its version.
func scanTask(row rowScanner) (tasks.TaskWithVersion, error) {
	var (
		id           int64
		title        string
		note         sql.NullString
		priority     string
		status       string
		plannedDate  any
		dueDate      any
		dueAt        sql.NullInt64
		timezone     string
		tags         sql.NullString
		createdAt    int64
		updatedAt    int64
		completedAt  sql.NullInt64
		cancelledAt  sql.NullInt64
		parentTaskID sql.NullInt64
		version      int
	)

	if err := row.Scan(
		&id, &title, &note, &priority, &status, &plannedDate, &dueDate, &dueAt,
		&timezone, &tags, &createdAt, &updatedAt, &completedAt, &cancelledAt,
		&parentTaskID, &version,
	); err != nil {
		return tasks.TaskWithVersion{}, err
	}

	decodedTags, err := decodeTags(tags)
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("task %d: %w", id, err)
	}

	planned, err := decodeCalendarDate(plannedDate)
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("task %d planned_date: %w", id, err)
	}
	due, err := decodeCalendarDate(dueDate)
	if err != nil {
		return tasks.TaskWithVersion{}, fmt.Errorf("task %d due_date: %w", id, err)
	}

	aggregate := &task.Task{
		ID:           id,
		Title:        title,
		Note:         stringPtr(note),
		Priority:     task.Priority(priority),
		Status:       task.Status(status),
		PlannedDate:  planned,
		DueDate:      due,
		DueAt:        timePtr(dueAt),
		Timezone:     timezone,
		Tags:         decodedTags,
		CreatedAt:    timeFromUnix(createdAt),
		UpdatedAt:    timeFromUnix(updatedAt),
		CompletedAt:  timePtr(completedAt),
		CancelledAt:  timePtr(cancelledAt),
		ParentTaskID: int64Ptr(parentTaskID),
	}

	if err := aggregate.Validate(); err != nil {
		// A row that cannot be validated means the database was written by
		// something other than this repository. Surface it instead of returning
		// a half-valid aggregate.
		return tasks.TaskWithVersion{}, fmt.Errorf("task %d: invalid stored row: %w", id, err)
	}

	return tasks.TaskWithVersion{Task: aggregate, Version: version}, nil
}

func encodeTags(tags []string) (string, error) {
	if tags == nil {
		tags = []string{}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return "", fmt.Errorf("encode tags: %w", err)
	}
	return string(encoded), nil
}

func decodeTags(value sql.NullString) ([]string, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(value.String), &tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

func encodeStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func encodeCalendarDate(value *task.CalendarDate) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.String()
}

// decodeCalendarDate accepts the shapes SQLite drivers may return for a DATE
// column. modernc.org/sqlite hands back time.Time for declared DATE columns,
// while rows written by plain SQL may come back as text; both must decode to
// the same timezone-neutral calendar date.
func decodeCalendarDate(value any) (*task.CalendarDate, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case time.Time:
		date, err := task.NewCalendarDate(typed.Year(), typed.Month(), typed.Day())
		if err != nil {
			return nil, err
		}
		return &date, nil
	case string:
		return parseCalendarText(typed)
	case []byte:
		return parseCalendarText(string(typed))
	default:
		return nil, fmt.Errorf("unsupported date storage type %T", value)
	}
}

func parseCalendarText(value string) (*task.CalendarDate, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 10 {
		value = value[:10]
	}
	parsed, err := task.ParseCalendarDate(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func encodeTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return unixSeconds(*value)
}

func timePtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := timeFromUnix(value.Int64)
	return &t
}

func encodeParentID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func unixSeconds(t time.Time) int64 {
	return t.UTC().Unix()
}

func timeFromUnix(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}

// escapeLike escapes LIKE wildcards so user search text is matched literally.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	value = strings.ReplaceAll(value, "_", `\_`)
	return value
}

// page slices a fully filtered result set for the overdue path.
func page(items []tasks.TaskWithVersion, limit, offset int) []tasks.TaskWithVersion {
	if offset >= len(items) {
		return []tasks.TaskWithVersion{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
