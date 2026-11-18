package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// ResearchUnitOfWork implements research.UnitOfWork on SQLite.
type ResearchUnitOfWork struct{ db *sql.DB }

func NewResearchUnitOfWork(db *sql.DB) *ResearchUnitOfWork {
	return &ResearchUnitOfWork{db: db}
}

func (u *ResearchUnitOfWork) Do(ctx context.Context, fn func(context.Context, appresearch.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, &researchTransaction{tx: tx})
	})
}

type researchTransaction struct{ tx *sql.Tx }

func (t *researchTransaction) Research() appresearch.Repository {
	return &researchRepository{tx: t.tx}
}

type researchRepository struct{ tx *sql.Tx }

// ─── Reports ────────────────────────────────────────────────────────────────

const reportColumns = `id, question, status, queries_json, summary, findings_json, sources_count,
	knowledge_entry_id, error, started_at, finished_at, version`

func scanReport(row rowScanner) (*domainresearch.Report, error) {
	var (
		report       domainresearch.Report
		status       string
		queriesJSON  string
		findingsJSON string
		knowledgeID  sql.NullInt64
		startedAt    int64
		finishedAt   sql.NullInt64
	)
	if err := row.Scan(&report.ID, &report.Question, &status, &queriesJSON, &report.Summary,
		&findingsJSON, &report.Sources, &knowledgeID, &report.Error,
		&startedAt, &finishedAt, &report.Version); err != nil {
		return nil, err
	}
	report.Status = domainresearch.ReportStatus(status)
	if err := json.Unmarshal([]byte(queriesJSON), &report.Queries); err != nil {
		return nil, fmt.Errorf("decode research %d queries: %w", report.ID, err)
	}
	if err := json.Unmarshal([]byte(findingsJSON), &report.Findings); err != nil {
		return nil, fmt.Errorf("decode research %d findings: %w", report.ID, err)
	}
	if knowledgeID.Valid {
		value := knowledgeID.Int64
		report.KnowledgeEntryID = &value
	}
	report.StartedAt = timeFromUnix(startedAt)
	report.FinishedAt = unixPtr(finishedAt)
	return &report, nil
}

func encodeJSON(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
}

// InsertReport stores a new research run.
func (r *researchRepository) InsertReport(ctx context.Context, report *domainresearch.Report) (int64, error) {
	if err := report.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO research_reports (question, status, queries_json, summary, findings_json,
			sources_count, knowledge_entry_id, error, started_at, finished_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.Question, string(report.Status), encodeJSON(report.Queries, "[]"),
		report.Summary, encodeJSON(report.Findings, "[]"), report.Sources,
		nullableID(report.KnowledgeEntryID), report.Error,
		unixSeconds(report.StartedAt), unixNullableTime(report.FinishedAt), report.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert research report: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert research report: read id: %w", err)
	}
	report.ID = id
	return id, nil
}

// GetReport returns one stored report.
func (r *researchRepository) GetReport(ctx context.Context, id int64) (*domainresearch.Report, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+reportColumns+` FROM research_reports WHERE id = ?`, id)
	report, err := scanReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appresearch.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get research report %d: %w", id, err)
	}
	return report, nil
}

// ListReports returns a page of reports, newest first.
func (r *researchRepository) ListReports(ctx context.Context, filter appresearch.ReportFilter) ([]*domainresearch.Report, int, error) {
	conditions := []string{"1 = 1"}
	args := []any{}
	if strings.TrimSpace(filter.Query) != "" {
		conditions = append(conditions, `(question LIKE ? OR summary LIKE ?)`)
		pattern := "%" + escapeLike(filter.Query) + "%"
		args = append(args, pattern, pattern)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM research_reports WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count research reports: %w", err)
	}
	limit, offset := pageLimit(filter.Limit, filter.Offset)
	args = append(args, limit, offset)
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+reportColumns+` FROM research_reports WHERE `+where+
			` ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list research reports: %w", err)
	}
	defer rows.Close()
	found := make([]*domainresearch.Report, 0)
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, 0, err
		}
		found = append(found, report)
	}
	return found, total, rows.Err()
}

// UpdateReport performs a conditional write and bumps the version.
func (r *researchRepository) UpdateReport(ctx context.Context, id int64, expectedVersion int, report *domainresearch.Report) (*domainresearch.Report, error) {
	if err := report.Validate(); err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE research_reports SET
			question = ?, status = ?, queries_json = ?, summary = ?, findings_json = ?,
			sources_count = ?, knowledge_entry_id = ?, error = ?, started_at = ?, finished_at = ?,
			version = version + 1
		WHERE id = ? AND version = ?`,
		report.Question, string(report.Status), encodeJSON(report.Queries, "[]"),
		report.Summary, encodeJSON(report.Findings, "[]"), report.Sources,
		nullableID(report.KnowledgeEntryID), report.Error,
		unixSeconds(report.StartedAt), unixNullableTime(report.FinishedAt),
		id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update research report %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update research report %d: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.GetReport(ctx, id); errors.Is(err, appresearch.ErrNotFound) {
			return nil, appresearch.ErrNotFound
		}
		return nil, appresearch.ErrVersionConflict
	}
	return r.GetReport(ctx, id)
}

// DeleteReport removes a report.
func (r *researchRepository) DeleteReport(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM research_reports WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete research report %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete research report %d: %w", id, err)
	}
	if affected == 0 {
		return appresearch.ErrNotFound
	}
	return nil
}

func nullableID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// encodeToolsJSON stores the user-approved tool names of a source.
func encodeToolsJSON(tools []string) (string, error) {
	if len(tools) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return "", fmt.Errorf("encode allowed tools: %w", err)
	}
	return string(encoded), nil
}

// decodeToolsJSON reads the user-approved tool names of a source.
func decodeToolsJSON(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var tools []string
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		return nil, fmt.Errorf("decode allowed tools: %w", err)
	}
	return tools, nil
}

// ─── Connections ────────────────────────────────────────────────────────────

const connectionColumns = `id, name, kind, mode, trust, format, endpoint, notes, enabled,
	allowed_tools, created_by_assistant, last_used_at, created_at, updated_at, version`

func scanConnection(row rowScanner) (*domainresearch.Connection, error) {
	var (
		connection  domainresearch.Connection
		kind        string
		mode        string
		trust       string
		format      string
		enabled     int
		byAssistant int
		lastUsedAt  sql.NullInt64
		createdAt   int64
		updatedAt   int64
		toolsJSON   string
	)
	if err := row.Scan(&connection.ID, &connection.Name, &kind, &mode, &trust, &format,
		&connection.Endpoint, &connection.Notes, &enabled, &toolsJSON, &byAssistant,
		&lastUsedAt, &createdAt, &updatedAt, &connection.Version); err != nil {
		return nil, err
	}
	tools, err := decodeToolsJSON(toolsJSON)
	if err != nil {
		return nil, err
	}
	connection.AllowedTools = tools
	connection.Kind = domainresearch.SourceKind(kind)
	connection.Mode = domainresearch.AccessMode(mode)
	connection.Trust = domainresearch.Trust(trust)
	connection.Format = domainresearch.SearchFormat(format)
	connection.Enabled = enabled != 0
	connection.CreatedByAssistant = byAssistant != 0
	connection.LastUsedAt = unixPtr(lastUsedAt)
	connection.CreatedAt = timeFromUnix(createdAt)
	connection.UpdatedAt = timeFromUnix(updatedAt)
	return &connection, nil
}

// InsertConnection stores one external source.
func (r *researchRepository) InsertConnection(ctx context.Context, connection *domainresearch.Connection) (int64, error) {
	if err := connection.Validate(); err != nil {
		return 0, err
	}
	toolsJSON, err := encodeToolsJSON(connection.AllowedTools)
	if err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO research_connections (name, kind, mode, trust, format, endpoint, notes,
			enabled, allowed_tools, created_by_assistant, last_used_at, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.Name, string(connection.Kind), string(connection.Mode), string(connection.Trust),
		string(connection.Format), connection.Endpoint, connection.Notes,
		boolToInt(connection.Enabled), toolsJSON, boolToInt(connection.CreatedByAssistant),
		unixNullableTime(connection.LastUsedAt),
		unixSeconds(connection.CreatedAt), unixSeconds(connection.UpdatedAt), connection.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert research connection: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert research connection: read id: %w", err)
	}
	connection.ID = id
	return id, nil
}

// GetConnection returns one source.
func (r *researchRepository) GetConnection(ctx context.Context, id int64) (*domainresearch.Connection, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM research_connections WHERE id = ?`, id)
	connection, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appresearch.ErrSourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get research connection %d: %w", id, err)
	}
	return connection, nil
}

// ListConnections returns the connected sources.
func (r *researchRepository) ListConnections(ctx context.Context, filter appresearch.ConnectionFilter) ([]*domainresearch.Connection, error) {
	conditions := []string{"1 = 1"}
	args := []any{}
	if filter.Enabled != nil {
		conditions = append(conditions, `enabled = ?`)
		args = append(args, boolToInt(*filter.Enabled))
	}
	if filter.Kind != nil {
		conditions = append(conditions, `kind = ?`)
		args = append(args, string(*filter.Kind))
	}
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+connectionColumns+` FROM research_connections WHERE `+
			strings.Join(conditions, " AND ")+` ORDER BY name COLLATE NOCASE ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list research connections: %w", err)
	}
	defer rows.Close()
	found := make([]*domainresearch.Connection, 0)
	for rows.Next() {
		connection, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, connection)
	}
	return found, rows.Err()
}

// UpdateConnection performs a conditional write and bumps the version.
func (r *researchRepository) UpdateConnection(ctx context.Context, id int64, expectedVersion int, connection *domainresearch.Connection) (*domainresearch.Connection, error) {
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	toolsJSON, err := encodeToolsJSON(connection.AllowedTools)
	if err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE research_connections SET
			name = ?, kind = ?, mode = ?, trust = ?, format = ?, endpoint = ?, notes = ?,
			enabled = ?, allowed_tools = ?, created_by_assistant = ?, last_used_at = ?, updated_at = ?,
			version = version + 1
		WHERE id = ? AND version = ?`,
		connection.Name, string(connection.Kind), string(connection.Mode), string(connection.Trust),
		string(connection.Format), connection.Endpoint, connection.Notes,
		boolToInt(connection.Enabled), toolsJSON, boolToInt(connection.CreatedByAssistant),
		unixNullableTime(connection.LastUsedAt), unixSeconds(connection.UpdatedAt),
		id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update research connection %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update research connection %d: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.GetConnection(ctx, id); errors.Is(err, appresearch.ErrSourceNotFound) {
			return nil, appresearch.ErrSourceNotFound
		}
		return nil, appresearch.ErrVersionConflict
	}
	return r.GetConnection(ctx, id)
}

// DeleteConnection removes a source. Its audit journal is kept: a deleted
// source must still explain what it was used for.
func (r *researchRepository) DeleteConnection(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM research_connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete research connection %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete research connection %d: %w", id, err)
	}
	if affected == 0 {
		return appresearch.ErrSourceNotFound
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ─── Watches ────────────────────────────────────────────────────────────────

const watchColumns = `id, topic, query, queries_json, cadence, enabled, next_check_at,
	last_checked_at, last_change_at, last_change_summary, seen_json, created_at, updated_at, version`

func scanWatch(row rowScanner) (*domainresearch.Watch, error) {
	var (
		watch         domainresearch.Watch
		queriesJSON   string
		cadence       string
		enabled       int
		nextCheckAt   sql.NullInt64
		lastCheckedAt sql.NullInt64
		lastChangeAt  sql.NullInt64
		seenJSON      string
		createdAt     int64
		updatedAt     int64
	)
	if err := row.Scan(&watch.ID, &watch.Topic, &watch.Query, &queriesJSON, &cadence, &enabled,
		&nextCheckAt, &lastCheckedAt, &lastChangeAt, &watch.LastChangeSummary, &seenJSON,
		&createdAt, &updatedAt, &watch.Version); err != nil {
		return nil, err
	}
	watch.Cadence = domainresearch.Cadence(cadence)
	watch.Enabled = enabled != 0
	watch.NextCheckAt = unixPtr(nextCheckAt)
	watch.LastCheckedAt = unixPtr(lastCheckedAt)
	watch.LastChangeAt = unixPtr(lastChangeAt)
	if err := json.Unmarshal([]byte(queriesJSON), &watch.Queries); err != nil {
		return nil, fmt.Errorf("decode watch %d queries: %w", watch.ID, err)
	}
	if err := json.Unmarshal([]byte(seenJSON), &watch.Seen); err != nil {
		return nil, fmt.Errorf("decode watch %d baseline: %w", watch.ID, err)
	}
	watch.CreatedAt = timeFromUnix(createdAt)
	watch.UpdatedAt = timeFromUnix(updatedAt)
	return &watch, nil
}

// InsertWatch stores one standing question.
func (r *researchRepository) InsertWatch(ctx context.Context, watch *domainresearch.Watch) (int64, error) {
	if err := watch.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO research_watches (topic, query, queries_json, cadence, enabled, next_check_at,
			last_checked_at, last_change_at, last_change_summary, seen_json, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		watch.Topic, watch.Query, encodeJSON(watch.Queries, "[]"), string(watch.Cadence),
		boolToInt(watch.Enabled), unixNullableTime(watch.NextCheckAt),
		unixNullableTime(watch.LastCheckedAt), unixNullableTime(watch.LastChangeAt),
		watch.LastChangeSummary, encodeJSON(watch.Seen, "[]"),
		unixSeconds(watch.CreatedAt), unixSeconds(watch.UpdatedAt), watch.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert research watch: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert research watch: read id: %w", err)
	}
	watch.ID = id
	return id, nil
}

// GetWatch returns one watch.
func (r *researchRepository) GetWatch(ctx context.Context, id int64) (*domainresearch.Watch, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+watchColumns+` FROM research_watches WHERE id = ?`, id)
	watch, err := scanWatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appresearch.ErrWatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get research watch %d: %w", id, err)
	}
	return watch, nil
}

// ListWatches returns the watches, newest change first.
func (r *researchRepository) ListWatches(ctx context.Context, includeDisabled bool) ([]*domainresearch.Watch, error) {
	query := `SELECT ` + watchColumns + ` FROM research_watches`
	if !includeDisabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY COALESCE(last_change_at, created_at) DESC, id DESC`
	rows, err := r.tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list research watches: %w", err)
	}
	defer rows.Close()
	found := make([]*domainresearch.Watch, 0)
	for rows.Next() {
		watch, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, watch)
	}
	return found, rows.Err()
}

// DueWatches returns the enabled watches whose moment has come.
func (r *researchRepository) DueWatches(ctx context.Context, now time.Time) ([]*domainresearch.Watch, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT `+watchColumns+` FROM research_watches
		WHERE enabled = 1 AND cadence <> 'manual'
		  AND (next_check_at IS NULL OR next_check_at <= ?)
		ORDER BY next_check_at ASC, id ASC`,
		unixSeconds(now))
	if err != nil {
		return nil, fmt.Errorf("list due research watches: %w", err)
	}
	defer rows.Close()
	found := make([]*domainresearch.Watch, 0)
	for rows.Next() {
		watch, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, watch)
	}
	return found, rows.Err()
}

// UpdateWatch performs a conditional write and bumps the version.
func (r *researchRepository) UpdateWatch(ctx context.Context, id int64, expectedVersion int, watch *domainresearch.Watch) (*domainresearch.Watch, error) {
	if err := watch.Validate(); err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE research_watches SET
			topic = ?, query = ?, queries_json = ?, cadence = ?, enabled = ?, next_check_at = ?,
			last_checked_at = ?, last_change_at = ?, last_change_summary = ?, seen_json = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		watch.Topic, watch.Query, encodeJSON(watch.Queries, "[]"), string(watch.Cadence),
		boolToInt(watch.Enabled), unixNullableTime(watch.NextCheckAt),
		unixNullableTime(watch.LastCheckedAt), unixNullableTime(watch.LastChangeAt),
		watch.LastChangeSummary, encodeJSON(watch.Seen, "[]"),
		unixSeconds(watch.UpdatedAt), id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update research watch %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update research watch %d: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.GetWatch(ctx, id); errors.Is(err, appresearch.ErrWatchNotFound) {
			return nil, appresearch.ErrWatchNotFound
		}
		return nil, appresearch.ErrVersionConflict
	}
	return r.GetWatch(ctx, id)
}

// DeleteWatch stops watching a topic.
func (r *researchRepository) DeleteWatch(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM research_watches WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete research watch %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete research watch %d: %w", id, err)
	}
	if affected == 0 {
		return appresearch.ErrWatchNotFound
	}
	return nil
}

// ─── Audit journal ──────────────────────────────────────────────────────────

// RecordCall appends one interaction with an external source and returns its
// id, so a changing call can be closed when its answer arrives.
func (r *researchRepository) RecordCall(ctx context.Context, record *appresearch.CallRecord) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO research_calls (connection_id, name, kind, mode, method, operation, outcome, detail, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ConnectionID, record.Name, string(record.Kind), string(record.Mode),
		record.Method, record.Operation, record.Outcome, record.Detail, record.DurationMS,
		unixSeconds(record.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("record research call: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("record research call: %w", err)
	}
	return id, nil
}

// FinishCall closes a write-ahead journal entry with what actually happened.
func (r *researchRepository) FinishCall(ctx context.Context, id int64, outcome, detail string, durationMS int64) error {
	updated, err := r.tx.ExecContext(ctx, `
		UPDATE research_calls SET outcome = ?, detail = ?, duration_ms = ?
		WHERE id = ? AND outcome = ?`,
		outcome, detail, durationMS, id, appresearch.CallStarted,
	)
	if err != nil {
		return fmt.Errorf("finish research call: %w", err)
	}
	if affected, err := updated.RowsAffected(); err == nil && affected == 0 {
		return appresearch.ErrNotFound
	}
	return nil
}

// RecoverRunningReports marks reports a previous process left running as
// failed. A report that never finished must say so, not stay "в работе" for
// ever.
func (r *researchRepository) RecoverRunningReports(ctx context.Context, reason string, now time.Time) (int64, error) {
	updated, err := r.tx.ExecContext(ctx, `
		UPDATE research_reports
		SET status = ?, error = ?, summary = ?, finished_at = ?, version = version + 1
		WHERE status = ?`,
		string(domainresearch.ReportFailed), reason, "## Прервано\n\n"+reason,
		unixSeconds(now), string(domainresearch.ReportRunning),
	)
	if err != nil {
		return 0, fmt.Errorf("recover running reports: %w", err)
	}
	return updated.RowsAffected()
}

// RecoverStartedCalls marks write intents without an outcome as unknown: the
// external service may well have accepted the change.
func (r *researchRepository) RecoverStartedCalls(ctx context.Context, detail string, now time.Time) (int64, error) {
	updated, err := r.tx.ExecContext(ctx, `
		UPDATE research_calls SET outcome = ?, detail = ?
		WHERE outcome = ?`,
		appresearch.CallUnknown, detail, appresearch.CallStarted,
	)
	if err != nil {
		return 0, fmt.Errorf("recover started research calls: %w", err)
	}
	return updated.RowsAffected()
}

// ListCalls returns the journal of one source, newest first. A zero id lists
// the whole journal.
func (r *researchRepository) ListCalls(ctx context.Context, connectionID int64, limit int) ([]*appresearch.CallRecord, int, error) {
	where := "1 = 1"
	args := []any{}
	if connectionID > 0 {
		where = "connection_id = ?"
		args = append(args, connectionID)
	}
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM research_calls WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count research calls: %w", err)
	}
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)
	rows, err := r.tx.QueryContext(ctx, `
		SELECT id, connection_id, name, kind, mode, method, operation, outcome, detail, duration_ms, created_at
		FROM research_calls WHERE `+where+`
		ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list research calls: %w", err)
	}
	defer rows.Close()
	found := make([]*appresearch.CallRecord, 0, limit)
	for rows.Next() {
		var (
			record    appresearch.CallRecord
			kind      string
			mode      string
			createdAt int64
		)
		if err := rows.Scan(&record.ID, &record.ConnectionID, &record.Name, &kind, &mode,
			&record.Method, &record.Operation, &record.Outcome, &record.Detail, &record.DurationMS, &createdAt); err != nil {
			return nil, 0, err
		}
		record.Kind = domainresearch.SourceKind(kind)
		record.Mode = domainresearch.AccessMode(mode)
		record.CreatedAt = timeFromUnix(createdAt)
		found = append(found, &record)
	}
	return found, total, rows.Err()
}
