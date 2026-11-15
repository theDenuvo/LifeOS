package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// GoalUnitOfWork implements goals.UnitOfWork on SQLite.
type GoalUnitOfWork struct{ db *sql.DB }

func NewGoalUnitOfWork(db *sql.DB) *GoalUnitOfWork { return &GoalUnitOfWork{db: db} }

func (u *GoalUnitOfWork) Do(ctx context.Context, fn func(context.Context, appgoals.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, goalTransaction{tx: tx})
	})
}

type goalTransaction struct{ tx *sql.Tx }

func (t goalTransaction) Goals() appgoals.Repository { return &goalRepository{tx: t.tx} }

type goalRepository struct{ tx *sql.Tx }

const goalColumns = `id, title, description, status, target_date, approved_at, created_at, updated_at, version`

func scanGoal(row rowScanner) (*domaingoal.Goal, error) {
	var (
		g           domaingoal.Goal
		status      string
		targetDate  sql.NullInt64
		approvedAt  sql.NullInt64
		createdAt   int64
		updatedAt   int64
	)
	if err := row.Scan(&g.ID, &g.Title, &g.Description, &status, &targetDate, &approvedAt, &createdAt, &updatedAt, &g.Version); err != nil {
		return nil, err
	}
	g.Status = domaingoal.Status(status)
	g.TargetDate = unixPtr(targetDate)
	g.ApprovedAt = unixPtr(approvedAt)
	g.CreatedAt = timeFromUnix(createdAt)
	g.UpdatedAt = timeFromUnix(updatedAt)
	return &g, nil
}

func unixPtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := timeFromUnix(value.Int64)
	return &t
}

// InsertGoal stores the goal and its initial plan.
func (r *goalRepository) InsertGoal(ctx context.Context, goal *domaingoal.Goal, milestones []domaingoal.MilestoneInput) (int64, error) {
	if err := goal.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO goals (title, description, status, target_date, approved_at, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		goal.Title, goal.Description, string(goal.Status),
		unixNullableTime(goal.TargetDate), unixNullableTime(goal.ApprovedAt),
		unixSeconds(goal.CreatedAt), unixSeconds(goal.UpdatedAt), goal.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert goal: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert goal: read id: %w", err)
	}
	now := timeFromUnix(unixSeconds(goal.CreatedAt))
	for index, input := range milestones {
		milestone := &domaingoal.Milestone{
			GoalID: id, Position: index, Title: strings.TrimSpace(input.Title),
			Criteria: strings.TrimSpace(input.Criteria), DueDate: input.DueDate,
			Status: domaingoal.MilestonePending, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		if err := domaingoal.ValidateMilestoneInput(input); err != nil {
			return 0, err
		}
		if _, err := r.InsertMilestone(ctx, milestone); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (r *goalRepository) GetGoal(ctx context.Context, id int64) (*domaingoal.Goal, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+goalColumns+` FROM goals WHERE id = ?`, id)
	goal, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appgoals.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get goal %d: %w", id, err)
	}
	return goal, nil
}

// UpdateGoal performs a conditional write and bumps the version.
func (r *goalRepository) UpdateGoal(ctx context.Context, id int64, expectedVersion int, goal *domaingoal.Goal) (*domaingoal.Goal, error) {
	if err := goal.Validate(); err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE goals SET
			title = ?, description = ?, status = ?, target_date = ?, approved_at = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		goal.Title, goal.Description, string(goal.Status),
		unixNullableTime(goal.TargetDate), unixNullableTime(goal.ApprovedAt),
		unixSeconds(goal.UpdatedAt), id, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update goal %d: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		if _, err := r.GetGoal(ctx, id); errors.Is(err, appgoals.ErrNotFound) {
			return nil, appgoals.ErrNotFound
		}
		return nil, appgoals.ErrVersionConflict
	}
	return r.GetGoal(ctx, id)
}

func (r *goalRepository) DeleteGoal(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete goal %d: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return appgoals.ErrNotFound
	}
	return nil
}

// ListGoals bounds listing in SQL; search matches title substrings.
func (r *goalRepository) ListGoals(ctx context.Context, filter appgoals.Filter) ([]*domaingoal.Goal, int, error) {
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if filter.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, string(*filter.Status))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		conditions = append(conditions, "title LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(query)+"%")
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM goals`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count goals: %w", err)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 30
	}
	args = append(args, limit, filter.Offset)
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+goalColumns+` FROM goals`+where+` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()
	goals := make([]*domaingoal.Goal, 0, 16)
	for rows.Next() {
		goal, err := scanGoal(rows)
		if err != nil {
			return nil, 0, err
		}
		goals = append(goals, goal)
	}
	return goals, total, rows.Err()
}

func (r *goalRepository) CountByStatus(ctx context.Context) (map[domaingoal.Status]int, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT status, COUNT(*) FROM goals GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count goals by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[domaingoal.Status]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[domaingoal.Status(status)] = count
	}
	return counts, rows.Err()
}

// InsertMilestone appends one plan step at the end of the current order.
func (r *goalRepository) InsertMilestone(ctx context.Context, milestone *domaingoal.Milestone) (int64, error) {
	if err := milestone.Validate(); err != nil {
		return 0, err
	}
	var position int
	if err := r.tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM goal_milestones WHERE goal_id = ?`, milestone.GoalID).Scan(&position); err != nil {
		return 0, fmt.Errorf("next milestone position: %w", err)
	}
	milestone.Position = position
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO goal_milestones (goal_id, position, title, criteria, status, due_date, completed_at, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		milestone.GoalID, position, milestone.Title, milestone.Criteria, string(milestone.Status),
		unixNullableTime(milestone.DueDate), unixNullableTime(milestone.CompletedAt),
		unixSeconds(milestone.CreatedAt), unixSeconds(milestone.UpdatedAt), milestone.Version,
	)
	if err != nil {
		return 0, fmt.Errorf("insert goal milestone: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert goal milestone: read id: %w", err)
	}
	return id, nil
}

const milestoneColumns = `id, goal_id, position, title, criteria, status, due_date, completed_at, created_at, updated_at, version`

func scanMilestone(row rowScanner) (*domaingoal.Milestone, error) {
	var (
		m           domaingoal.Milestone
		status      string
		dueDate     sql.NullInt64
		completedAt sql.NullInt64
		createdAt   int64
		updatedAt   int64
	)
	if err := row.Scan(&m.ID, &m.GoalID, &m.Position, &m.Title, &m.Criteria, &status, &dueDate, &completedAt, &createdAt, &updatedAt, &m.Version); err != nil {
		return nil, err
	}
	m.Status = domaingoal.MilestoneStatus(status)
	m.DueDate = unixPtr(dueDate)
	m.CompletedAt = unixPtr(completedAt)
	m.CreatedAt = timeFromUnix(createdAt)
	m.UpdatedAt = timeFromUnix(updatedAt)
	return &m, nil
}

func (r *goalRepository) ListMilestones(ctx context.Context, goalID int64) ([]domaingoal.Milestone, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+milestoneColumns+` FROM goal_milestones WHERE goal_id = ? ORDER BY position, id`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal milestones: %w", err)
	}
	defer rows.Close()
	milestones := make([]domaingoal.Milestone, 0, 8)
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		milestones = append(milestones, *m)
	}
	return milestones, rows.Err()
}

func (r *goalRepository) GetMilestone(ctx context.Context, goalID, milestoneID int64) (*domaingoal.Milestone, error) {
	row := r.tx.QueryRowContext(ctx,
		`SELECT `+milestoneColumns+` FROM goal_milestones WHERE id = ? AND goal_id = ?`, milestoneID, goalID)
	m, err := scanMilestone(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appgoals.ErrMilestoneNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get goal milestone %d: %w", milestoneID, err)
	}
	return m, nil
}

func (r *goalRepository) UpdateMilestone(ctx context.Context, milestoneID int64, expectedVersion int, milestone *domaingoal.Milestone) (*domaingoal.Milestone, error) {
	if err := milestone.Validate(); err != nil {
		return nil, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE goal_milestones SET
			position = ?, title = ?, criteria = ?, status = ?, due_date = ?, completed_at = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		milestone.Position, milestone.Title, milestone.Criteria, string(milestone.Status),
		unixNullableTime(milestone.DueDate), unixNullableTime(milestone.CompletedAt),
		unixSeconds(milestone.UpdatedAt), milestoneID, expectedVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("update goal milestone %d: %w", milestoneID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		if _, err := r.GetMilestone(ctx, milestone.GoalID, milestoneID); errors.Is(err, appgoals.ErrMilestoneNotFound) {
			return nil, appgoals.ErrMilestoneNotFound
		}
		return nil, appgoals.ErrVersionConflict
	}
	return r.GetMilestone(ctx, milestone.GoalID, milestoneID)
}

func (r *goalRepository) DeleteMilestone(ctx context.Context, goalID, milestoneID int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM goal_milestones WHERE id = ? AND goal_id = ?`, milestoneID, goalID)
	if err != nil {
		return fmt.Errorf("delete goal milestone %d: %w", milestoneID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return appgoals.ErrMilestoneNotFound
	}
	return nil
}

func (r *goalRepository) InsertLink(ctx context.Context, link *domaingoal.Link) (int64, error) {
	if err := link.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO goal_links (goal_id, target_type, target_id, relation, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(goal_id, target_type, target_id, relation) DO NOTHING`,
		link.GoalID, string(link.TargetType), link.TargetID, link.Relation, unixSeconds(link.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert goal link: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		if err := r.tx.QueryRowContext(ctx, `
			SELECT id FROM goal_links WHERE goal_id = ? AND target_type = ? AND target_id = ? AND relation = ?`,
			link.GoalID, string(link.TargetType), link.TargetID, link.Relation).Scan(&id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (r *goalRepository) ListLinks(ctx context.Context, goalID int64) ([]domaingoal.Link, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT id, goal_id, target_type, target_id, relation, created_at
		FROM goal_links WHERE goal_id = ? ORDER BY id ASC`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal links: %w", err)
	}
	defer rows.Close()
	links := make([]domaingoal.Link, 0, 4)
	for rows.Next() {
		var (
			link       domaingoal.Link
			targetType string
			createdAt  int64
		)
		if err := rows.Scan(&link.ID, &link.GoalID, &targetType, &link.TargetID, &link.Relation, &createdAt); err != nil {
			return nil, err
		}
		link.TargetType = domaingoal.TargetType(targetType)
		link.CreatedAt = timeFromUnix(createdAt)
		links = append(links, link)
	}
	return links, rows.Err()
}

func (r *goalRepository) DeleteLink(ctx context.Context, goalID, linkID int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM goal_links WHERE id = ? AND goal_id = ?`, linkID, goalID)
	if err != nil {
		return fmt.Errorf("delete goal link %d: %w", linkID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return appgoals.ErrLinkNotFound
	}
	return nil
}

// TargetExists verifies a link target using the current transaction; the same
// single-writer reasoning as the memory repository applies.
func (r *goalRepository) TargetExists(ctx context.Context, target domaingoal.TargetType, id int64) (bool, error) {
	table := ""
	switch target {
	case domaingoal.TargetTask:
		table = "tasks"
	case domaingoal.TargetEvent:
		table = "calendar_events"
	case domaingoal.TargetConversation:
		table = "conversations"
	case domaingoal.TargetDocument:
		table = "documents"
	case domaingoal.TargetMemory:
		table = "memory_entries"
	case domaingoal.TargetInbox:
		table = "inbox_items"
	default:
		return false, nil
	}
	var found int
	err := r.tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check goal link target: %w", err)
	}
	return true, nil
}

func (r *goalRepository) InsertProgress(ctx context.Context, event *domaingoal.ProgressEvent) (int64, error) {
	if err := event.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO goal_progress (goal_id, kind, summary, source_type, source_id, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		event.GoalID, string(event.Kind), event.Summary,
		nullableString(sourceString(event.SourceType)), nullableInt64Value(event.SourceID),
		unixSeconds(event.RecordedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert goal progress: %w", err)
	}
	return result.LastInsertId()
}

func sourceString(t *domaingoal.TargetType) string {
	if t == nil {
		return ""
	}
	return string(*t)
}

func nullableInt64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

const progressColumns = `id, goal_id, kind, summary, source_type, source_id, recorded_at`

func scanProgress(row rowScanner) (*domaingoal.ProgressEvent, error) {
	var (
		e          domaingoal.ProgressEvent
		kind       string
		sourceType sql.NullString
		sourceID   sql.NullInt64
		recordedAt int64
	)
	if err := row.Scan(&e.ID, &e.GoalID, &kind, &e.Summary, &sourceType, &sourceID, &recordedAt); err != nil {
		return nil, err
	}
	e.Kind = domaingoal.ProgressKind(kind)
	if sourceType.Valid && sourceType.String != "" {
		t := domaingoal.TargetType(sourceType.String)
		e.SourceType = &t
		id := sourceID.Int64
		e.SourceID = &id
	}
	e.RecordedAt = timeFromUnix(recordedAt)
	return &e, nil
}

func (r *goalRepository) ListProgress(ctx context.Context, goalID int64, limit int) ([]domaingoal.ProgressEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+progressColumns+` FROM goal_progress WHERE goal_id = ? ORDER BY recorded_at DESC, id DESC LIMIT ?`,
		goalID, limit)
	if err != nil {
		return nil, fmt.Errorf("list goal progress: %w", err)
	}
	defer rows.Close()
	events := make([]domaingoal.ProgressEvent, 0, 16)
	for rows.Next() {
		e, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, rows.Err()
}

func (r *goalRepository) LastActivity(ctx context.Context, goalID int64) (*time.Time, error) {
	var last sql.NullInt64
	err := r.tx.QueryRowContext(ctx,
		`SELECT MAX(recorded_at) FROM goal_progress WHERE goal_id = ?`, goalID).Scan(&last)
	if err != nil {
		return nil, fmt.Errorf("last goal activity %d: %w", goalID, err)
	}
	if !last.Valid {
		return nil, nil
	}
	t := timeFromUnix(last.Int64)
	return &t, nil
}

const goalProposalColumns = `id, goal_id, summary, payload, status, created_at, decided_at`

func (r *goalRepository) InsertProposal(ctx context.Context, proposal *domaingoal.Proposal) (int64, error) {
	if err := proposal.Validate(); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(proposal.Payload)
	if err != nil {
		return 0, fmt.Errorf("encode goal proposal payload: %w", err)
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO goal_proposals (goal_id, summary, payload, status, created_at, decided_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		proposal.GoalID, proposal.Summary, string(payload), string(proposal.Status),
		unixSeconds(proposal.CreatedAt), unixNullableTime(proposal.DecidedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert goal proposal: %w", err)
	}
	return result.LastInsertId()
}

func scanGoalProposal(row rowScanner) (*domaingoal.Proposal, error) {
	var (
		p         domaingoal.Proposal
		payload   string
		status    string
		createdAt int64
		decidedAt sql.NullInt64
	)
	if err := row.Scan(&p.ID, &p.GoalID, &p.Summary, &payload, &status, &createdAt, &decidedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(payload), &p.Payload); err != nil {
		return nil, fmt.Errorf("decode goal proposal payload: %w", err)
	}
	p.Status = domaingoal.ProposalStatus(status)
	p.CreatedAt = timeFromUnix(createdAt)
	p.DecidedAt = unixPtr(decidedAt)
	return &p, nil
}

func (r *goalRepository) GetProposal(ctx context.Context, goalID, proposalID int64) (*domaingoal.Proposal, error) {
	row := r.tx.QueryRowContext(ctx,
		`SELECT `+goalProposalColumns+` FROM goal_proposals WHERE id = ? AND goal_id = ?`, proposalID, goalID)
	p, err := scanGoalProposal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appgoals.ErrProposalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get goal proposal %d: %w", proposalID, err)
	}
	return p, nil
}

func (r *goalRepository) ListProposals(ctx context.Context, goalID int64, pendingOnly bool) ([]domaingoal.Proposal, error) {
	where := " WHERE goal_id = ?"
	args := []any{goalID}
	if pendingOnly {
		where += " AND status = 'pending'"
	}
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+goalProposalColumns+` FROM goal_proposals`+where+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal proposals: %w", err)
	}
	defer rows.Close()
	proposals := make([]domaingoal.Proposal, 0, 4)
	for rows.Next() {
		p, err := scanGoalProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, *p)
	}
	return proposals, rows.Err()
}

func (r *goalRepository) SetProposalStatus(ctx context.Context, proposalID int64, status domaingoal.ProposalStatus, at time.Time) error {
	result, err := r.tx.ExecContext(ctx,
		`UPDATE goal_proposals SET status = ?, decided_at = ? WHERE id = ? AND status = 'pending'`,
		string(status), unixSeconds(at), proposalID)
	if err != nil {
		return fmt.Errorf("update goal proposal %d: %w", proposalID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return appgoals.ErrNoPendingProposal
	}
	return nil
}

func (r *goalRepository) HasPendingProposal(ctx context.Context, goalID int64) (bool, error) {
	var count int
	err := r.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal_proposals WHERE goal_id = ? AND status = 'pending'`, goalID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check pending goal proposal: %w", err)
	}
	return count > 0, nil
}

// unixNullableTime stores an optional instant as unix seconds.
func unixNullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return unixSeconds(*t)
}

