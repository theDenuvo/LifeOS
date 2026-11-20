package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appskills "github.com/theDenuvo/LifeOS/internal/application/skills"
	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// SkillUnitOfWork implements skills.UnitOfWork on SQLite.
type SkillUnitOfWork struct{ db *sql.DB }

func NewSkillUnitOfWork(db *sql.DB) *SkillUnitOfWork { return &SkillUnitOfWork{db: db} }

func (u *SkillUnitOfWork) Do(ctx context.Context, fn func(context.Context, appskills.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, skillTransaction{tx: tx})
	})
}

type skillTransaction struct{ tx *sql.Tx }

func (t skillTransaction) Skills() appskills.Repository { return &skillRepository{tx: t.tx} }

// Settings exposes the settings store on the transaction ApplyProposal
// already owns, so a working-instructions proposal and its resolution
// commit together. Opening a second transaction here (via
// settings.Service) would deadlock on SetMaxOpenConns(1).
func (t skillTransaction) Settings() appskills.SettingsRepository {
	return &settingsRepository{tx: t.tx}
}

type skillRepository struct{ tx *sql.Tx }

const skillColumns = `id, name, purpose, when_to_use, instructions, limitations, examples,
	tags_json, status, current_version, source, created_by_assistant,
	use_count, good_count, bad_count, last_used_at, created_at, updated_at, version`

func scanSkill(row rowScanner) (*domainskill.Skill, error) {
	var (
		s          domainskill.Skill
		tagsJSON   string
		status     string
		source     string
		lastUsedAt sql.NullInt64
		createdAt  int64
		updatedAt  int64
		createdBy  int64
	)
	if err := row.Scan(&s.ID, &s.Name, &s.Purpose, &s.WhenToUse, &s.Instructions, &s.Limitations, &s.Examples,
		&tagsJSON, &status, &s.CurrentVersion, &source, &createdBy,
		&s.UseCount, &s.GoodCount, &s.BadCount, &lastUsedAt, &createdAt, &updatedAt, &s.Version); err != nil {
		return nil, err
	}
	s.Status = domainskill.Status(status)
	s.Source = domainskill.Source(source)
	s.CreatedByAssistant = createdBy != 0
	if err := json.Unmarshal([]byte(tagsJSON), &s.Tags); err != nil {
		return nil, fmt.Errorf("decode skill %d tags: %w", s.ID, err)
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	s.LastUsedAt = unixPtr(lastUsedAt)
	s.CreatedAt = timeFromUnix(createdAt)
	s.UpdatedAt = timeFromUnix(updatedAt)
	return &s, nil
}

func encodeSkillTags(tags []string) (string, error) {
	if tags == nil {
		tags = []string{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "", fmt.Errorf("encode skill tags: %w", err)
	}
	return string(data), nil
}

func encodeStrings(values []string) string {
	if values == nil {
		values = []string{}
	}
	data, _ := json.Marshal(values)
	return string(data)
}

func decodeStrings(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil || values == nil {
		return []string{}
	}
	return values
}

func (r *skillRepository) insertVersion(ctx context.Context, skillID int64, version domainskill.Version) error {
	tagsJSON, err := encodeSkillTags(version.Content.Tags)
	if err != nil {
		return err
	}
	_, err = r.tx.ExecContext(ctx, `
		INSERT INTO skill_versions (skill_id, version_no, name, purpose, when_to_use, instructions,
			limitations, examples, tags_json, change_note, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		skillID, version.VersionNo, version.Content.Name, version.Content.Purpose, version.Content.WhenToUse,
		version.Content.Instructions, version.Content.Limitations, version.Content.Examples, tagsJSON,
		version.ChangeNote, string(version.CreatedBy), unixSeconds(version.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert skill version: %w", err)
	}
	return nil
}

func (r *skillRepository) InsertSkill(ctx context.Context, skill *domainskill.Skill, first domainskill.Version) (int64, error) {
	if err := domainskill.ValidateContent(skill.Content()); err != nil {
		return 0, err
	}
	tagsJSON, err := encodeSkillTags(skill.Tags)
	if err != nil {
		return 0, err
	}
	createdBy := int64(0)
	if skill.CreatedByAssistant {
		createdBy = 1
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO skills (name, normalized_name, purpose, when_to_use, instructions, limitations, examples,
			tags_json, status, current_version, source, created_by_assistant,
			use_count, good_count, bad_count, last_used_at, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		skill.Name, domainskill.NormalizeName(skill.Name), skill.Purpose, skill.WhenToUse, skill.Instructions,
		skill.Limitations, skill.Examples, tagsJSON, string(skill.Status), skill.CurrentVersion, string(skill.Source),
		createdBy, skill.UseCount, skill.GoodCount, skill.BadCount, unixNullableTime(skill.LastUsedAt),
		unixSeconds(skill.CreatedAt), unixSeconds(skill.UpdatedAt), skill.Version)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, appskills.ErrVersionConflict
		}
		return 0, fmt.Errorf("insert skill: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert skill: read id: %w", err)
	}
	skill.ID = id
	first.SkillID = id
	if err := r.insertVersion(ctx, id, first); err != nil {
		return 0, err
	}
	return id, nil
}

func (r *skillRepository) GetSkill(ctx context.Context, id int64) (*domainskill.Skill, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+skillColumns+` FROM skills WHERE id = ?`, id)
	skill, err := scanSkill(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appskills.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill %d: %w", id, err)
	}
	return skill, nil
}

func (r *skillRepository) GetSkillByName(ctx context.Context, normalized string) (*domainskill.Skill, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+skillColumns+` FROM skills WHERE normalized_name = ?`, normalized)
	skill, err := scanSkill(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appskills.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill by name: %w", err)
	}
	return skill, nil
}

func (r *skillRepository) ListSkills(ctx context.Context, filter appskills.Filter) ([]*domainskill.Skill, int, error) {
	conditions := []string{"1 = 1"}
	args := []any{}
	if strings.TrimSpace(filter.Query) != "" {
		conditions = append(conditions, `(name LIKE ? OR purpose LIKE ? OR when_to_use LIKE ? OR instructions LIKE ?)`)
		pattern := "%" + escapeLike(filter.Query) + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}
	if filter.Status != nil {
		conditions = append(conditions, `status = ?`)
		args = append(args, string(*filter.Status))
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count skills: %w", err)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 30
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := r.tx.QueryContext(ctx, `SELECT `+skillColumns+` FROM skills WHERE `+where+` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()
	var out []*domainskill.Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, skill)
	}
	return out, total, rows.Err()
}

func (r *skillRepository) UpdateSkill(ctx context.Context, id int64, expectedVersion int, skill *domainskill.Skill, newVersion *domainskill.Version) (*domainskill.Skill, error) {
	tagsJSON, err := encodeSkillTags(skill.Tags)
	if err != nil {
		return nil, err
	}
	createdBy := int64(0)
	if skill.CreatedByAssistant {
		createdBy = 1
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE skills SET name = ?, normalized_name = ?, purpose = ?, when_to_use = ?, instructions = ?,
			limitations = ?, examples = ?, tags_json = ?, status = ?, current_version = ?,
			source = ?, created_by_assistant = ?, use_count = ?, good_count = ?, bad_count = ?,
			last_used_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		skill.Name, domainskill.NormalizeName(skill.Name), skill.Purpose, skill.WhenToUse, skill.Instructions,
		skill.Limitations, skill.Examples, tagsJSON, string(skill.Status), skill.CurrentVersion, string(skill.Source),
		createdBy, skill.UseCount, skill.GoodCount, skill.BadCount, unixNullableTime(skill.LastUsedAt),
		unixSeconds(skill.UpdatedAt), id, expectedVersion)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, appskills.ErrVersionConflict
		}
		return nil, fmt.Errorf("update skill: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update skill: read affected: %w", err)
	}
	if affected == 0 {
		if _, err := r.GetSkill(ctx, id); err != nil {
			return nil, err
		}
		return nil, appskills.ErrVersionConflict
	}
	if newVersion != nil {
		if err := r.insertVersion(ctx, id, *newVersion); err != nil {
			return nil, err
		}
	}
	return r.GetSkill(ctx, id)
}

func (r *skillRepository) DeleteSkill(ctx context.Context, id int64) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	return nil
}

const skillVersionColumns = `id, skill_id, version_no, name, purpose, when_to_use, instructions,
	limitations, examples, tags_json, change_note, created_by, created_at`

func scanSkillVersion(row rowScanner) (*domainskill.Version, error) {
	var (
		v         domainskill.Version
		tagsJSON  string
		createdBy string
		createdAt int64
	)
	if err := row.Scan(&v.ID, &v.SkillID, &v.VersionNo, &v.Content.Name, &v.Content.Purpose, &v.Content.WhenToUse,
		&v.Content.Instructions, &v.Content.Limitations, &v.Content.Examples, &tagsJSON, &v.ChangeNote, &createdBy, &createdAt); err != nil {
		return nil, err
	}
	v.Content.Tags = decodeStrings(tagsJSON)
	v.CreatedBy = domainskill.Source(createdBy)
	v.CreatedAt = timeFromUnix(createdAt)
	return &v, nil
}

func (r *skillRepository) ListVersions(ctx context.Context, skillID int64) ([]domainskill.Version, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT `+skillVersionColumns+` FROM skill_versions WHERE skill_id = ? ORDER BY version_no ASC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	var out []domainskill.Version
	for rows.Next() {
		v, err := scanSkillVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (r *skillRepository) GetVersion(ctx context.Context, skillID int64, versionNo int) (*domainskill.Version, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+skillVersionColumns+` FROM skill_versions WHERE skill_id = ? AND version_no = ?`, skillID, versionNo)
	v, err := scanSkillVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appskills.ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill version: %w", err)
	}
	return v, nil
}

const skillProposalColumns = `id, skill_id, kind, status, problem, evidence_json,
	proposed_name, proposed_purpose, proposed_when, proposed_instructions, proposed_limitations,
	proposed_examples, proposed_tags_json, expected_effect, test_scenarios_json, test_result,
	base_version, base_working_hash, conversation_id, result_skill_id, created_at, resolved_at, version`

func scanSkillProposal(row rowScanner) (*domainskill.Proposal, error) {
	var (
		p               domainskill.Proposal
		skillID         sql.NullInt64
		kind, status    string
		evidenceJSON    string
		scenariosJSON   string
		tagsJSON        string
		baseVersion     sql.NullInt64
		baseWorkingHash sql.NullString
		conversationID  sql.NullInt64
		resultSkillID   sql.NullInt64
		createdAt       int64
		resolvedAt      sql.NullInt64
	)
	if err := row.Scan(&p.ID, &skillID, &kind, &status, &p.Problem, &evidenceJSON,
		&p.Proposed.Name, &p.Proposed.Purpose, &p.Proposed.WhenToUse, &p.Proposed.Instructions,
		&p.Proposed.Limitations, &p.Proposed.Examples, &tagsJSON, &p.ExpectedEffect,
		&scenariosJSON, &p.TestResult, &baseVersion, &baseWorkingHash, &conversationID, &resultSkillID,
		&createdAt, &resolvedAt, &p.Version); err != nil {
		return nil, err
	}
	p.Kind = domainskill.ProposalKind(kind)
	p.Status = domainskill.ProposalStatus(status)
	p.Evidence = decodeStrings(evidenceJSON)
	p.TestScenarios = decodeStrings(scenariosJSON)
	p.Proposed.Tags = decodeStrings(tagsJSON)
	if skillID.Valid {
		v := skillID.Int64
		p.SkillID = &v
	}
	if baseWorkingHash.Valid {
		v := baseWorkingHash.String
		p.BaseWorkingHash = &v
	}
	if baseVersion.Valid {
		v := int(baseVersion.Int64)
		p.BaseVersion = &v
	}
	if conversationID.Valid {
		v := conversationID.Int64
		p.ConversationID = &v
	}
	if resultSkillID.Valid {
		v := resultSkillID.Int64
		p.ResultSkillID = &v
	}
	p.CreatedAt = timeFromUnix(createdAt)
	p.ResolvedAt = unixPtr(resolvedAt)
	return &p, nil
}

func (r *skillRepository) InsertProposal(ctx context.Context, proposal *domainskill.Proposal) (int64, error) {
	tagsJSON, err := encodeSkillTags(proposal.Proposed.Tags)
	if err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO skill_proposals (skill_id, kind, status, problem, evidence_json,
			proposed_name, proposed_purpose, proposed_when, proposed_instructions, proposed_limitations,
			proposed_examples, proposed_tags_json, expected_effect, test_scenarios_json, test_result,
			base_version, base_working_hash, conversation_id, result_skill_id, created_at, resolved_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.SkillID, string(proposal.Kind), string(proposal.Status), proposal.Problem,
		encodeStrings(proposal.Evidence), proposal.Proposed.Name, proposal.Proposed.Purpose,
		proposal.Proposed.WhenToUse, proposal.Proposed.Instructions, proposal.Proposed.Limitations,
		proposal.Proposed.Examples, tagsJSON, proposal.ExpectedEffect, encodeStrings(proposal.TestScenarios),
		proposal.TestResult, proposal.BaseVersion, proposal.BaseWorkingHash, proposal.ConversationID, proposal.ResultSkillID,
		unixSeconds(proposal.CreatedAt), unixNullableTime(proposal.ResolvedAt), proposal.Version)
	if err != nil {
		return 0, fmt.Errorf("insert skill proposal: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert skill proposal: read id: %w", err)
	}
	proposal.ID = id
	return id, nil
}

func (r *skillRepository) GetProposal(ctx context.Context, id int64) (*domainskill.Proposal, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+skillProposalColumns+` FROM skill_proposals WHERE id = ?`, id)
	proposal, err := scanSkillProposal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appskills.ErrProposalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill proposal %d: %w", id, err)
	}
	return proposal, nil
}

func (r *skillRepository) ListProposals(ctx context.Context, skillID int64, status *domainskill.ProposalStatus) ([]*domainskill.Proposal, error) {
	conditions := []string{"1 = 1"}
	args := []any{}
	if skillID != 0 {
		conditions = append(conditions, `skill_id = ?`)
		args = append(args, skillID)
	}
	if status != nil {
		conditions = append(conditions, `status = ?`)
		args = append(args, string(*status))
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT `+skillProposalColumns+` FROM skill_proposals WHERE `+strings.Join(conditions, " AND ")+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list skill proposals: %w", err)
	}
	defer rows.Close()
	var out []*domainskill.Proposal
	for rows.Next() {
		p, err := scanSkillProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *skillRepository) UpdateProposal(ctx context.Context, id int64, expectedVersion int, proposal *domainskill.Proposal) (*domainskill.Proposal, error) {
	result, err := r.tx.ExecContext(ctx, `
		UPDATE skill_proposals SET status = ?, result_skill_id = ?, resolved_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		string(proposal.Status), proposal.ResultSkillID, unixNullableTime(proposal.ResolvedAt), id, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("update skill proposal: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update skill proposal: read affected: %w", err)
	}
	if affected == 0 {
		if _, err := r.GetProposal(ctx, id); err != nil {
			return nil, err
		}
		return nil, appskills.ErrVersionConflict
	}
	return r.GetProposal(ctx, id)
}

func (r *skillRepository) RecordFeedback(ctx context.Context, feedback *domainskill.Feedback) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO skill_feedback (skill_id, version_no, rating, comment, conversation_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		feedback.SkillID, feedback.VersionNo, string(feedback.Rating), feedback.Comment, feedback.ConversationID, unixSeconds(feedback.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert skill feedback: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert skill feedback: read id: %w", err)
	}
	feedback.ID = id
	column := "good_count"
	if feedback.Rating == domainskill.RatingBad {
		column = "bad_count"
	}
	if _, err := r.tx.ExecContext(ctx, `UPDATE skills SET `+column+` = `+column+` + 1 WHERE id = ?`, feedback.SkillID); err != nil {
		return 0, fmt.Errorf("count skill feedback: %w", err)
	}
	return id, nil
}

func (r *skillRepository) ListFeedback(ctx context.Context, skillID int64, limit int) ([]domainskill.Feedback, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT id, skill_id, version_no, rating, comment, conversation_id, created_at
		FROM skill_feedback WHERE skill_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, skillID, limit)
	if err != nil {
		return nil, fmt.Errorf("list skill feedback: %w", err)
	}
	defer rows.Close()
	var out []domainskill.Feedback
	for rows.Next() {
		var (
			f              domainskill.Feedback
			rating         string
			conversationID sql.NullInt64
			createdAt      int64
		)
		if err := rows.Scan(&f.ID, &f.SkillID, &f.VersionNo, &rating, &f.Comment, &conversationID, &createdAt); err != nil {
			return nil, err
		}
		f.Rating = domainskill.Rating(rating)
		if conversationID.Valid {
			v := conversationID.Int64
			f.ConversationID = &v
		}
		f.CreatedAt = timeFromUnix(createdAt)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *skillRepository) RecordUsage(ctx context.Context, usage *domainskill.Usage) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO skill_usages (skill_id, version_no, conversation_id, context, matched_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		usage.SkillID, usage.VersionNo, usage.ConversationID, usage.Context, encodeStrings(usage.MatchedTerms), unixSeconds(usage.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert skill usage: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert skill usage: read id: %w", err)
	}
	usage.ID = id
	if _, err := r.tx.ExecContext(ctx, `UPDATE skills SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`, unixSeconds(usage.CreatedAt), usage.SkillID); err != nil {
		return 0, fmt.Errorf("count skill usage: %w", err)
	}
	return id, nil
}

func (r *skillRepository) ListUsages(ctx context.Context, skillID int64, limit int) ([]domainskill.Usage, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT id, skill_id, version_no, conversation_id, context, matched_json, created_at
		FROM skill_usages WHERE skill_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, skillID, limit)
	if err != nil {
		return nil, fmt.Errorf("list skill usages: %w", err)
	}
	defer rows.Close()
	var out []domainskill.Usage
	for rows.Next() {
		var (
			u              domainskill.Usage
			conversationID sql.NullInt64
			matchedJSON    string
			createdAt      int64
		)
		if err := rows.Scan(&u.ID, &u.SkillID, &u.VersionNo, &conversationID, &u.Context, &matchedJSON, &createdAt); err != nil {
			return nil, err
		}
		if conversationID.Valid {
			v := conversationID.Int64
			u.ConversationID = &v
		}
		u.MatchedTerms = decodeStrings(matchedJSON)
		u.CreatedAt = timeFromUnix(createdAt)
		out = append(out, u)
	}
	return out, rows.Err()
}
