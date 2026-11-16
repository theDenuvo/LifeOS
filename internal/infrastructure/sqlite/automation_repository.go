package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

// AutomationUnitOfWork implements automation.UnitOfWork on SQLite.
type AutomationUnitOfWork struct{ db *sql.DB }

func NewAutomationUnitOfWork(db *sql.DB) *AutomationUnitOfWork {
	return &AutomationUnitOfWork{db: db}
}

func (u *AutomationUnitOfWork) Do(ctx context.Context, fn func(context.Context, appautomation.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, &automationTransaction{tx: tx})
	})
}

type automationTransaction struct{ tx *sql.Tx }

func (t *automationTransaction) Automations() appautomation.Repository {
	return &automationRepository{tx: t.tx}
}

type automationRepository struct{ tx *sql.Tx }

const automationColumns = `id, title, description, status, trigger_kind, trigger_json, steps_json,
	expected_result, consequences, approved_at, last_run_at, next_run_at, created_at, updated_at, version`

func scanAutomation(row rowScanner) (*domainautomation.Automation, error) {
	var (
		a           domainautomation.Automation
		status      string
		triggerKind string
		triggerJSON string
		stepsJSON   string
		approvedAt  sql.NullInt64
		lastRunAt   sql.NullInt64
		nextRunAt   sql.NullInt64
		createdAt   int64
		updatedAt   int64
	)
	if err := row.Scan(&a.ID, &a.Title, &a.Description, &status, &triggerKind, &triggerJSON,
		&stepsJSON, &a.ExpectedResult, &a.Consequences,
		&approvedAt, &lastRunAt, &nextRunAt, &createdAt, &updatedAt, &a.Version); err != nil {
		return nil, err
	}
	a.Status = domainautomation.Status(status)
	a.Trigger.Kind = domainautomation.TriggerKind(triggerKind)
	if err := json.Unmarshal([]byte(triggerJSON), &a.Trigger); err != nil {
		return nil, fmt.Errorf("decode automation %d trigger: %w", a.ID, err)
	}
	a.Trigger.Kind = domainautomation.TriggerKind(triggerKind)
	if err := json.Unmarshal([]byte(stepsJSON), &a.Steps); err != nil {
		return nil, fmt.Errorf("decode automation %d steps: %w", a.ID, err)
	}
	a.ApprovedAt = unixPtr(approvedAt)
	a.LastRunAt = unixPtr(lastRunAt)
	a.NextRunAt = unixPtr(nextRunAt)
	a.CreatedAt = timeFromUnix(createdAt)
	a.UpdatedAt = timeFromUnix(updatedAt)
	return &a, nil
}

func encodeTrigger(trigger domainautomation.Trigger) (string, error) {
	data, err := json.Marshal(trigger)
	if err != nil {
		return "", fmt.Errorf("encode trigger: %w", err)
	}
	return string(data), nil
}

func encodeSteps(steps []domainautomation.Step) (string, error) {
	if steps == nil {
		steps = []domainautomation.Step{}
	}
	data, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("encode steps: %w", err)
	}
	return string(data), nil
}

// InsertAutomation stores the aggregate and returns its id.
func (r *automationRepository) Insert(ctx context.Context, automation *domainautomation.Automation) (int64, error) {
	if err := automation.Validate(); err != nil {
		return 0, err
	}
	triggerJSON, err := encodeTrigger(automation.Trigger)
	if err != nil {
		return 0, err
	}
	stepsJSON, err := encodeSteps(automation.Steps)
	if err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO automations (title, description, status, trigger_kind, trigger_json, steps_json,
			expected_result, consequences, approved_at, last_run_at, next_run_at, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		automation.Title, automation.Description, string(automation.Status), string(automation.Trigger.Kind),
		triggerJSON, stepsJSON, automation.ExpectedResult, automation.Consequences,
		unixNullableTime(automation.ApprovedAt), unixNullableTime(automation.LastRunAt),
		unixNullableTime(automation.NextRunAt),
		unixSeconds(automation.CreatedAt), unixSeconds(automation.UpdatedAt), automation.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert automation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert automation: read id: %w", err)
	}
	automation.ID = id
	return id, nil
}

func (r *automationRepository) Get(ctx context.Context, id int64) (*domainautomation.Automation, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+automationColumns+` FROM automations WHERE id = ?`, id)
	automation, err := scanAutomation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appautomation.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get automation %d: %w", id, err)
	}
	return automation, nil
}

func (r *automationRepository) List(ctx context.Context, filter appautomation.Filter) ([]*domainautomation.Automation, int, error) {
	conditions := []string{"1 = 1"}
	args := []any{}
	if strings.TrimSpace(filter.Query) != "" {
		conditions = append(conditions, `(title LIKE ? OR description LIKE ?)`)
		pattern := "%" + escapeLike(filter.Query) + "%"
		args = append(args, pattern, pattern)
	}
	if filter.Status != nil {
		conditions = append(conditions, `status = ?`)
		args = append(args, string(*filter.Status))
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM automations WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count automations: %w", err)
	}
	limit, offset := pageLimit(filter.Limit, filter.Offset)
	args = append(args, limit, offset)
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+automationColumns+` FROM automations WHERE `+where+
			` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list automations: %w", err)
	}
	defer rows.Close()
	found := make([]*domainautomation.Automation, 0)
	for rows.Next() {
		automation, err := scanAutomation(rows)
		if err != nil {
			return nil, 0, err
		}
		found = append(found, automation)
	}
	return found, total, rows.Err()
}

func pageLimit(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// Update performs a conditional write and bumps the version.
func (r *automationRepository) Update(ctx context.Context, id int64, expectedVersion int, automation *domainautomation.Automation) (*domainautomation.Automation, error) {
	if err := automation.Validate(); err != nil {
		return nil, err
	}
	triggerJSON, err := encodeTrigger(automation.Trigger)
	if err != nil {
		return nil, err
	}
	stepsJSON, err := encodeSteps(automation.Steps)
	if err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE automations SET
			title = ?, description = ?, status = ?, trigger_kind = ?, trigger_json = ?, steps_json = ?,
			expected_result = ?, consequences = ?, approved_at = ?, last_run_at = ?, next_run_at = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		automation.Title, automation.Description, string(automation.Status), string(automation.Trigger.Kind),
		triggerJSON, stepsJSON, automation.ExpectedResult, automation.Consequences,
		unixNullableTime(automation.ApprovedAt), unixNullableTime(automation.LastRunAt),
		unixNullableTime(automation.NextRunAt),
		unixSeconds(automation.UpdatedAt),
		id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update automation %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update automation %d: %w", id, err)
	}
	if affected == 0 {
		current, err := r.Get(ctx, id)
		if errors.Is(err, appautomation.ErrNotFound) {
			return nil, appautomation.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		_ = current
		return nil, appautomation.ErrVersionConflict
	}
	return r.Get(ctx, id)
}

func (r *automationRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete automation %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete automation %d: %w", id, err)
	}
	if affected == 0 {
		return appautomation.ErrNotFound
	}
	return nil
}

// ScheduleDue returns active schedule automations whose moment has come or
// whose bookmark needs recomputing.
func (r *automationRepository) ScheduleDue(ctx context.Context, now time.Time) ([]*domainautomation.Automation, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+automationColumns+` FROM automations
		WHERE status = 'active' AND trigger_kind = 'schedule'
		  AND (next_run_at IS NULL OR next_run_at <= ?)
		ORDER BY next_run_at ASC, id ASC`,
		unixSeconds(now),
	)
	if err != nil {
		return nil, fmt.Errorf("list due automations: %w", err)
	}
	defer rows.Close()
	found := make([]*domainautomation.Automation, 0)
	for rows.Next() {
		automation, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, automation)
	}
	return found, rows.Err()
}

func (r *automationRepository) ListByTrigger(ctx context.Context, kind domainautomation.TriggerKind) ([]*domainautomation.Automation, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+automationColumns+` FROM automations
		WHERE status = 'active' AND trigger_kind = ?
		ORDER BY id ASC`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list automations by trigger %s: %w", kind, err)
	}
	defer rows.Close()
	found := make([]*domainautomation.Automation, 0)
	for rows.Next() {
		automation, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, automation)
	}
	return found, rows.Err()
}

func encodeRunSteps(steps []domainautomation.RunStep) string {
	if steps == nil {
		steps = []domainautomation.RunStep{}
	}
	data, err := json.Marshal(steps)
	if err != nil {
		// A run without its step list is worse than an empty list: the
		// journal row is already reserved.
		return "[]"
	}
	return string(data)
}

func scanRun(row rowScanner) (*domainautomation.Run, error) {
	var (
		run        domainautomation.Run
		stepsJSON  string
		finishedAt sql.NullInt64
		startedAt  int64
	)
	if err := row.Scan(&run.ID, &run.AutomationID, &run.Trigger, &run.Status,
		&stepsJSON, &run.Summary, &run.Error, &startedAt, &finishedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stepsJSON), &run.Steps); err != nil {
		return nil, fmt.Errorf("decode run %d steps: %w", run.ID, err)
	}
	run.StartedAt = timeFromUnix(startedAt)
	run.FinishedAt = unixPtr(finishedAt)
	return &run, nil
}

const runColumns = `id, automation_id, trigger, status, steps_json, summary, error, started_at, finished_at`

// StartRun reserves the automation for one execution: the insert happens only
// when no run of this automation is still running.
func (r *automationRepository) StartRun(ctx context.Context, automationID int64, trigger string, startedAt time.Time) (*domainautomation.Run, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO automation_runs (automation_id, trigger, status, started_at)
		SELECT ?, ?, 'running', ?
		WHERE NOT EXISTS (
			SELECT 1 FROM automation_runs WHERE automation_id = ? AND status = 'running'
		)`,
		automationID, trigger, unixSeconds(startedAt), automationID,
	)
	if err != nil {
		return nil, fmt.Errorf("start automation run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("start automation run: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("start automation run: read id: %w", err)
	}
	return &domainautomation.Run{
		ID: id, AutomationID: automationID, Trigger: trigger,
		Status: domainautomation.RunRunning, StartedAt: startedAt,
	}, nil
}

// FinishRun stores the journal and the final state of the run.
func (r *automationRepository) FinishRun(ctx context.Context, run *domainautomation.Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE automation_runs SET status = ?, steps_json = ?, summary = ?, error = ?, finished_at = ?
		WHERE id = ? AND automation_id = ? AND status = 'running'`,
		string(run.Status), encodeRunSteps(run.Steps), run.Summary, run.Error,
		unixNullableTime(run.FinishedAt), run.ID, run.AutomationID,
	)
	if err != nil {
		return fmt.Errorf("finish automation run %d: %w", run.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish automation run %d: %w", run.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("finish automation run %d: %w", run.ID, appautomation.ErrRunNotFound)
	}
	return nil
}

// FailRun closes a reserved run as failed without a step journal. It is the
// best-effort release when the normal finish write failed: the reservation
// must not outlive the process because of one failed update.
func (r *automationRepository) FailRun(ctx context.Context, runID int64, reason string, at time.Time) error {
	result, err := r.tx.ExecContext(ctx, `
		UPDATE automation_runs SET status = 'failed', error = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		reason, unixSeconds(at), runID,
	)
	if err != nil {
		return fmt.Errorf("fail automation run %d: %w", runID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("fail automation run %d: %w", runID, err)
	}
	if affected == 0 {
		return fmt.Errorf("fail automation run %d: %w", runID, appautomation.ErrRunNotFound)
	}
	return nil
}

// RecoverRunning releases every reservation left running by an earlier
// process. Single-process LifeOS means all of them are stale at startup.
func (r *automationRepository) RecoverRunning(ctx context.Context, at time.Time) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		UPDATE automation_runs
		SET status = 'failed',
		    error = 'выполнение прервано: LifeOS был перезапущен во время работы сценария',
		    finished_at = ?
		WHERE status = 'running'`,
		unixSeconds(at),
	)
	if err != nil {
		return 0, fmt.Errorf("recover automation runs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover automation runs: %w", err)
	}
	return affected, nil
}

// RecordSkipped stores a terminal journal entry for a launch that was
// refused before any step ran.
func (r *automationRepository) RecordSkipped(ctx context.Context, automationID int64, trigger, reason string, at time.Time) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO automation_runs (automation_id, trigger, status, summary, error, started_at, finished_at)
		VALUES (?, ?, 'skipped', ?, ?, ?, ?)`,
		automationID, trigger, "Запуск не выполнен", reason, unixSeconds(at), unixSeconds(at),
	)
	if err != nil {
		return 0, fmt.Errorf("record skipped automation run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("record skipped automation run: read id: %w", err)
	}
	return id, nil
}

func (r *automationRepository) GetRun(ctx context.Context, automationID, runID int64) (*domainautomation.Run, error) {
	row := r.tx.QueryRowContext(ctx,
		`SELECT `+runColumns+` FROM automation_runs WHERE id = ? AND automation_id = ?`, runID, automationID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appautomation.ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get automation run %d: %w", runID, err)
	}
	return run, nil
}

func (r *automationRepository) ListRuns(ctx context.Context, automationID int64, limit, offset int) ([]*domainautomation.Run, int, error) {
	var total int
	if err := r.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM automation_runs WHERE automation_id = ?`, automationID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count automation runs: %w", err)
	}
	limitValue, offsetValue := pageLimit(limit, offset)
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+runColumns+` FROM automation_runs WHERE automation_id = ?
		ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`, automationID, limitValue, offsetValue)
	if err != nil {
		return nil, 0, fmt.Errorf("list automation runs: %w", err)
	}
	defer rows.Close()
	found := make([]*domainautomation.Run, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		found = append(found, run)
	}
	return found, total, rows.Err()
}
