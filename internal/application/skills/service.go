// Roadmap stage 8: skills and assistant self-improvement.
//
// The service keeps the product decisions where the roadmap puts them: the
// assistant drafts skills and proposes improvements, but an active skill
// changes only after the user applies a proposal (or edits it in the
// library, which is itself the explicit decision). Every applied change is
// a new version; a rollback is a new version too, so history is never
// rewritten. Feedback is recorded and feeds later analysis, but by itself
// it never rewrites an active instruction.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
	"github.com/theDenuvo/LifeOS/internal/events"
)

const (
	defaultLimit = 30
	maxLimit     = 100
	// catalogLimit bounds the always-injected catalogue so a large library
	// cannot crowd out the conversation; selection still finds any skill by
	// its conditions.
	catalogLimit   = 20
	analysisMaxAge = 30 * 24 * time.Hour
)

// Service coordinates the skill library, selection and self-improvement.
type Service struct {
	uow     UnitOfWork
	clock   Clock
	broker  events.Publisher
	working WorkingInstructionsStore
}

// Option customises the service.
type Option func(*Service)

// WithPublisher publishes skill changes to the event stream.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) { s.broker = publisher }
}

// WithWorkingInstructions attaches the store for working-instruction
// proposals.
func WithWorkingInstructions(store WorkingInstructionsStore) Option {
	return func(s *Service) { s.working = store }
}

// NewService builds the skill service.
func NewService(uow UnitOfWork, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	s := &Service{uow: uow, clock: clock}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// workingHash fingerprints a working-instructions text for the optimistic
// check: a proposal remembers the text it was prepared against and is
// refused when the text has changed since.
func workingHash(text string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(h[:])
}

// decodeWorkingText reads the JSON-encoded working instructions block the
// settings store keeps; an absent key means the empty block.
func decodeWorkingText(raw json.RawMessage, ok bool) (string, error) {
	if !ok {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", err
	}
	return text, nil
}

func (s *Service) publish(eventType string, payload map[string]any) {
	if s.broker != nil {
		s.broker.Publish(eventType, payload)
	}
}

// Create drafts a new skill. It always starts as a draft with version 1,
// whoever created it: activation is a separate user decision.
func (s *Service) Create(ctx context.Context, input CreateInput, source domainskill.Source) (*View, error) {
	if !source.IsValid() {
		source = domainskill.SourceUser
	}
	now := s.clock.Now().UTC()
	content := normalizeContent(input.Content)
	if err := domainskill.ValidateContent(content); err != nil {
		return nil, err
	}
	skill := &domainskill.Skill{
		Name: content.Name, Purpose: content.Purpose, WhenToUse: content.WhenToUse,
		Instructions: content.Instructions, Limitations: content.Limitations,
		Examples: content.Examples, Tags: content.Tags,
		Status: domainskill.StatusDraft, CurrentVersion: 1, Source: source,
		CreatedByAssistant: source == domainskill.SourceAssistant,
		CreatedAt:          now, UpdatedAt: now, Version: 1,
	}
	first := domainskill.Version{
		SkillID: 0, VersionNo: 1, Content: content,
		ChangeNote: strings.TrimSpace(input.ChangeNote), CreatedBy: source, CreatedAt: now,
	}
	var id int64
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		if existing, err := repo.GetSkillByName(ctx, domainskill.NormalizeName(content.Name)); err == nil && existing != nil {
			return fmt.Errorf("%w: навык с названием «%s» уже есть", ErrVersionConflict, content.Name)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		var err error
		id, err = repo.InsertSkill(ctx, skill, first)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.created", map[string]any{"id": id, "name": skill.Name, "status": string(skill.Status)})
	return s.Get(ctx, id)
}

// Get returns one skill with its versions, pending proposals and recent
// feedback and usage.
func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	var view *View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		found, err := repo.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		versions, err := repo.ListVersions(ctx, id)
		if err != nil {
			return err
		}
		proposals, err := repo.ListProposals(ctx, id, nil)
		if err != nil {
			return err
		}
		feedback, err := repo.ListFeedback(ctx, id, 10)
		if err != nil {
			return err
		}
		usages, err := repo.ListUsages(ctx, id, 10)
		if err != nil {
			return err
		}
		view = &View{Skill: found, Versions: versions, Proposals: proposals, Feedback: feedback, Usages: usages}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// List returns a page of skills, newest first.
func (s *Service) List(ctx context.Context, filter Filter) ([]*domainskill.Skill, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultLimit
	}
	if filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var (
		skills []*domainskill.Skill
		total  int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, count, err := tx.Skills().ListSkills(ctx, filter)
		if err != nil {
			return err
		}
		skills, total = found, count
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return skills, total, nil
}

// Update edits a skill. The user may edit an approved skill directly —
// the library is the user's instrument — and the edit becomes a new
// version, so the previous text stays comparable and restorable. The
// version guard keeps concurrent edits honest.
func (s *Service) Update(ctx context.Context, id int64, expectedVersion int, input UpdateInput, source domainskill.Source) (*View, error) {
	if !source.IsValid() {
		source = domainskill.SourceUser
	}
	now := s.clock.Now().UTC()
	var view *View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		current, err := repo.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		if current.Status == domainskill.StatusArchived {
			return fmt.Errorf("%w: архивный навык не изменяется; сначала восстановите его как черновик", ErrInvalidTransition)
		}
		next := current.Content()
		if input.Name != nil {
			next.Name = *input.Name
		}
		if input.Purpose != nil {
			next.Purpose = *input.Purpose
		}
		if input.WhenToUse != nil {
			next.WhenToUse = *input.WhenToUse
		}
		if input.Instructions != nil {
			next.Instructions = *input.Instructions
		}
		if input.Limitations != nil {
			next.Limitations = *input.Limitations
		}
		if input.Examples != nil {
			next.Examples = *input.Examples
		}
		if input.Tags != nil {
			next.Tags = *input.Tags
		}
		next = normalizeContent(next)
		if err := domainskill.ValidateContent(next); err != nil {
			return err
		}
		if sameContent(current.Content(), next) {
			return ErrEmptyUpdate
		}
		if domainskill.NormalizeName(next.Name) != domainskill.NormalizeName(current.Name) {
			if existing, err := repo.GetSkillByName(ctx, domainskill.NormalizeName(next.Name)); err == nil && existing != nil && existing.ID != id {
				return fmt.Errorf("%w: навык с названием «%s» уже есть", ErrVersionConflict, next.Name)
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		current.ApplyContent(next)
		current.CurrentVersion++
		current.UpdatedAt = now
		current.Version++
		version := &domainskill.Version{
			SkillID: id, VersionNo: current.CurrentVersion, Content: next,
			ChangeNote: strings.TrimSpace(input.ChangeNote), CreatedBy: source, CreatedAt: now,
		}
		updated, err := repo.UpdateSkill(ctx, id, expectedVersion, current, version)
		if err != nil {
			return err
		}
		view = &View{Skill: updated}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.updated", map[string]any{"id": id, "name": view.Skill.Name, "status": string(view.Skill.Status), "version": view.Skill.CurrentVersion})
	return s.Get(ctx, id)
}

// ChangeStatus moves a skill along its lifecycle. Activation, disabling
// and archiving are explicit user decisions; the service only refuses
// moves the lifecycle does not allow.
func (s *Service) ChangeStatus(ctx context.Context, id int64, expectedVersion int, to domainskill.Status) (*View, error) {
	if !to.IsValid() {
		return nil, fmt.Errorf("%w: неизвестный статус %q", ErrInvalidTransition, string(to))
	}
	now := s.clock.Now().UTC()
	var view *View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		current, err := repo.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		if !domainskill.CanTransition(current.Status, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, string(current.Status), string(to))
		}
		current.Status = to
		current.UpdatedAt = now
		current.Version++
		updated, err := repo.UpdateSkill(ctx, id, expectedVersion, current, nil)
		if err != nil {
			return err
		}
		view = &View{Skill: updated}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.updated", map[string]any{"id": id, "name": view.Skill.Name, "status": string(view.Skill.Status)})
	return s.Get(ctx, id)
}

// Delete removes a draft or archived skill with its history. An active or
// disabled skill must be archived first, so a delete is never the first
// thought applied to a skill that is still working.
func (s *Service) Delete(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		current, err := repo.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		if current.Status != domainskill.StatusDraft && current.Status != domainskill.StatusArchived {
			return ErrDeleteActive
		}
		return repo.DeleteSkill(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("skill.deleted", map[string]any{"id": id})
	return nil
}

// Rollback restores an earlier version as a new current version. History
// is append-only: the rolled-back text becomes the newest version, so the
// intermediate versions remain comparable.
func (s *Service) Rollback(ctx context.Context, id int64, expectedVersion int, versionNo int, changeNote string, source domainskill.Source) (*View, error) {
	if !source.IsValid() {
		source = domainskill.SourceUser
	}
	now := s.clock.Now().UTC()
	var view *View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		current, err := repo.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		if current.Status == domainskill.StatusArchived {
			return fmt.Errorf("%w: архивный навык сначала восстановите как черновик", ErrInvalidTransition)
		}
		target, err := repo.GetVersion(ctx, id, versionNo)
		if err != nil {
			return err
		}
		content := normalizeContent(target.Content)
		if err := domainskill.ValidateContent(content); err != nil {
			return err
		}
		if sameContent(current.Content(), content) {
			return ErrEmptyUpdate
		}
		current.ApplyContent(content)
		current.CurrentVersion++
		current.UpdatedAt = now
		current.Version++
		note := strings.TrimSpace(changeNote)
		if note == "" {
			note = fmt.Sprintf("возврат к версии %d", versionNo)
		}
		version := &domainskill.Version{
			SkillID: id, VersionNo: current.CurrentVersion, Content: content,
			ChangeNote: note, CreatedBy: source, CreatedAt: now,
		}
		updated, err := repo.UpdateSkill(ctx, id, expectedVersion, current, version)
		if err != nil {
			return err
		}
		view = &View{Skill: updated}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.updated", map[string]any{"id": id, "name": view.Skill.Name, "status": string(view.Skill.Status), "version": view.Skill.CurrentVersion})
	return s.Get(ctx, id)
}

// CreateProposal stores one prepared improvement. Nothing changes until
// the user applies it; the test result is computed now, before the
// decision, so the user sees whether the new text still connects.
func (s *Service) CreateProposal(ctx context.Context, input ProposalInput) (*domainskill.Proposal, error) {
	if !input.Kind.IsValid() {
		return nil, fmt.Errorf("неизвестный вид предложения %q", string(input.Kind))
	}
	proposed := normalizeContent(input.Proposed)
	if err := domainskill.ValidateProposal(input.Problem, input.ExpectedEffect, proposed, input.Kind); err != nil {
		return nil, err
	}
	// A skill-version proposal must name an existing skill and carry the
	// version it was prepared against; a create proposal must not reuse a
	// name that is already taken.
	var baseVersion *int
	var baseWorkingHash *string
	var current *domainskill.Skill
	if input.Kind == domainskill.ProposalSkillVersion {
		if input.SkillID == nil {
			return nil, fmt.Errorf("для предложения по навыку нужен его id")
		}
		var err error
		current, err = s.getSkill(ctx, *input.SkillID)
		if err != nil {
			return nil, err
		}
		v := current.CurrentVersion
		baseVersion = &v
	} else if input.Kind == domainskill.ProposalWorkingInstructions {
		if s.working != nil {
			current, err := s.working.GetWorkingInstructions(ctx)
			if err != nil {
				return nil, err
			}
			h := workingHash(current)
			baseWorkingHash = &h
		}
	} else if input.Kind == domainskill.ProposalSkillCreate {
		// Uniqueness is checked at apply time (the name may be freed by
		// then); here only the text is validated.
	}
	testResult := ""
	if current != nil {
		testResult = runTestScenarios(current.Content(), proposed, input.TestScenarios)
	} else {
		testResult = runTestScenarios(proposed, proposed, input.TestScenarios)
		if len(input.TestScenarios) == 0 {
			testResult = "новый навык: сценариев нет; проверена только допустимость текста"
		} else {
			testResult = fmt.Sprintf("новый навык: сценариев %d; проверена допустимость текста и покрытие сценариев названием", len(input.TestScenarios))
		}
	}
	if utf8.RuneCountInString(testResult) > domainskill.MaxTestRunes {
		testResult = string([]rune(testResult)[:domainskill.MaxTestRunes])
	}
	evidence := make([]string, 0, len(input.Evidence))
	for _, item := range input.Evidence {
		item = strings.TrimSpace(item)
		if item != "" {
			evidence = append(evidence, item)
		}
	}
	now := s.clock.Now().UTC()
	proposal := &domainskill.Proposal{
		SkillID: input.SkillID, Kind: input.Kind, Status: domainskill.ProposalProposed,
		Problem: strings.TrimSpace(input.Problem), Evidence: evidence,
		Proposed: proposed, ExpectedEffect: strings.TrimSpace(input.ExpectedEffect),
		TestScenarios: input.TestScenarios, TestResult: testResult,
		BaseVersion: baseVersion, BaseWorkingHash: baseWorkingHash, ConversationID: input.ConversationID,
		CreatedAt: now, Version: 1,
	}
	var id int64
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Skills().InsertProposal(ctx, proposal)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.proposal.created", map[string]any{"id": id, "skill_id": input.SkillID, "kind": string(input.Kind)})
	return s.GetProposal(ctx, id)
}

// GetProposal returns one proposal.
func (s *Service) GetProposal(ctx context.Context, id int64) (*domainskill.Proposal, error) {
	var proposal *domainskill.Proposal
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Skills().GetProposal(ctx, id)
		if err != nil {
			return err
		}
		proposal = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return proposal, nil
}

// ListProposals returns proposals, optionally for one skill and one status.
func (s *Service) ListProposals(ctx context.Context, skillID int64, status *domainskill.ProposalStatus) ([]*domainskill.Proposal, error) {
	var out []*domainskill.Proposal
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Skills().ListProposals(ctx, skillID, status)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyProposal applies one pending proposal. This is the controlled
// application the roadmap requires: the user decision is the apply call,
// the change becomes a new version, and a stale base is refused instead
// of overwriting a newer edit.
func (s *Service) ApplyProposal(ctx context.Context, proposalID int64, expectedProposalVersion int) (*domainskill.Proposal, *domainskill.Skill, error) {
	now := s.clock.Now().UTC()
	var (
		applied   *domainskill.Proposal
		skillView *domainskill.Skill
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		proposal, err := repo.GetProposal(ctx, proposalID)
		if err != nil {
			return err
		}
		if proposal.Status != domainskill.ProposalProposed {
			return ErrProposalResolved
		}
		switch proposal.Kind {
		case domainskill.ProposalSkillVersion:
			if proposal.SkillID == nil {
				return fmt.Errorf("у предложения нет навыка")
			}
			current, err := repo.GetSkill(ctx, *proposal.SkillID)
			if err != nil {
				return err
			}
			if current.Status == domainskill.StatusArchived {
				return fmt.Errorf("%w: архивный навык сначала восстановите как черновик", ErrInvalidTransition)
			}
			if proposal.BaseVersion != nil && *proposal.BaseVersion != current.CurrentVersion {
				return ErrStaleBase
			}
			content := normalizeContent(proposal.Proposed)
			if err := domainskill.ValidateContent(content); err != nil {
				return err
			}
			if sameContent(current.Content(), content) {
				return ErrEmptyUpdate
			}
			current.ApplyContent(content)
			current.CurrentVersion++
			current.UpdatedAt = now
			current.Version++
			version := &domainskill.Version{
				SkillID: current.ID, VersionNo: current.CurrentVersion, Content: content,
				ChangeNote: "применено предложение №" + fmt.Sprint(proposal.ID), CreatedBy: domainskill.SourceUser, CreatedAt: now,
			}
			updated, err := repo.UpdateSkill(ctx, current.ID, current.Version-1, current, version)
			if err != nil {
				return err
			}
			skillView = updated
		case domainskill.ProposalSkillCreate:
			content := normalizeContent(proposal.Proposed)
			if err := domainskill.ValidateContent(content); err != nil {
				return err
			}
			if existing, err := repo.GetSkillByName(ctx, domainskill.NormalizeName(content.Name)); err == nil && existing != nil {
				return fmt.Errorf("%w: навык с названием «%s» уже есть", ErrVersionConflict, content.Name)
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			skill := &domainskill.Skill{
				Name: content.Name, Purpose: content.Purpose, WhenToUse: content.WhenToUse,
				Instructions: content.Instructions, Limitations: content.Limitations,
				Examples: content.Examples, Tags: content.Tags,
				Status: domainskill.StatusDraft, CurrentVersion: 1,
				Source: domainskill.SourceAssistant, CreatedByAssistant: true,
				CreatedAt: now, UpdatedAt: now, Version: 1,
			}
			first := domainskill.Version{VersionNo: 1, Content: content, ChangeNote: "создан по предложению №" + fmt.Sprint(proposal.ID), CreatedBy: domainskill.SourceAssistant, CreatedAt: now}
			newID, err := repo.InsertSkill(ctx, skill, first)
			if err != nil {
				return err
			}
			resultID := newID
			proposal.ResultSkillID = &resultID
			skillView = skill
		case domainskill.ProposalWorkingInstructions:
			// The write joins this transaction via tx.Settings(). Going
			// through WorkingInstructionsStore (settings.Service) would
			// open a second transaction on the single SQLite connection
			// and hang here until the request is cancelled.
			settings := tx.Settings()
			if settings == nil {
				return ErrNoWorkingStore
			}
			raw, ok, err := settings.Get(ctx, WorkingInstructionsKey)
			if err != nil {
				return err
			}
			currentText, err := decodeWorkingText(raw, ok)
			if err != nil {
				return err
			}
			// A proposal prepared against a different text is stale:
			// refuse instead of overwriting the user's manual edits.
			// A nil fingerprint (pre-check proposals) applies as before.
			if proposal.BaseWorkingHash != nil && *proposal.BaseWorkingHash != workingHash(currentText) {
				return ErrStaleBase
			}
			text := strings.TrimSpace(proposal.Proposed.Instructions)
			if utf8.RuneCountInString(text) > domainskill.MaxInstructionsRunes {
				return domainskill.ErrInvalidInstructions
			}
			encoded, err := json.Marshal(text)
			if err != nil {
				return err
			}
			if err := settings.Put(ctx, WorkingInstructionsKey, encoded, now); err != nil {
				return err
			}
		default:
			return fmt.Errorf("неизвестный вид предложения %q", string(proposal.Kind))
		}
		proposal.Status = domainskill.ProposalApplied
		proposal.ResolvedAt = &now
		proposal.Version++
		updated, err := repo.UpdateProposal(ctx, proposal.ID, expectedProposalVersion, proposal)
		if err != nil {
			return err
		}
		applied = updated
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	s.publish("skill.proposal.resolved", map[string]any{"id": proposalID, "status": "applied"})
	if skillView != nil {
		s.publish("skill.updated", map[string]any{"id": skillView.ID, "name": skillView.Name, "status": string(skillView.Status), "version": skillView.CurrentVersion})
	}
	return applied, skillView, nil
}

// RejectProposal discards one pending proposal without changing anything.
func (s *Service) RejectProposal(ctx context.Context, proposalID int64, expectedProposalVersion int) (*domainskill.Proposal, error) {
	now := s.clock.Now().UTC()
	var rejected *domainskill.Proposal
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		proposal, err := repo.GetProposal(ctx, proposalID)
		if err != nil {
			return err
		}
		if proposal.Status != domainskill.ProposalProposed {
			return ErrProposalResolved
		}
		proposal.Status = domainskill.ProposalRejected
		proposal.ResolvedAt = &now
		proposal.Version++
		updated, err := repo.UpdateProposal(ctx, proposal.ID, expectedProposalVersion, proposal)
		if err != nil {
			return err
		}
		rejected = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.proposal.resolved", map[string]any{"id": proposalID, "status": "rejected"})
	return rejected, nil
}

// Rate records one user judgement. It moves the skill's counters; it
// never rewrites the instruction by itself.
func (s *Service) Rate(ctx context.Context, skillID int64, rating domainskill.Rating, comment string, conversationID *int64) (*domainskill.Feedback, error) {
	if !rating.IsValid() {
		return nil, fmt.Errorf("неизвестная оценка %q", string(rating))
	}
	if utf8.RuneCountInString(comment) > domainskill.MaxCommentRunes {
		return nil, domainskill.ErrInvalidComment
	}
	now := s.clock.Now().UTC()
	var (
		feedback *domainskill.Feedback
		skill    *domainskill.Skill
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		found, err := repo.GetSkill(ctx, skillID)
		if err != nil {
			return err
		}
		skill = found
		feedback = &domainskill.Feedback{
			SkillID: skillID, VersionNo: found.CurrentVersion, Rating: rating,
			Comment: strings.TrimSpace(comment), ConversationID: conversationID, CreatedAt: now,
		}
		_, err = repo.RecordFeedback(ctx, feedback)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("skill.feedback", map[string]any{"id": feedback.ID, "skill_id": skillID, "rating": string(rating)})
	_ = skill
	return feedback, nil
}

// ListFeedback returns recent judgements of one skill.
func (s *Service) ListFeedback(ctx context.Context, skillID int64, limit int) ([]domainskill.Feedback, error) {
	var out []domainskill.Feedback
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Skills().ListFeedback(ctx, skillID, limit)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListUsages returns recent injections of one skill.
func (s *Service) ListUsages(ctx context.Context, skillID int64, limit int) ([]domainskill.Usage, error) {
	var out []domainskill.Usage
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Skills().ListUsages(ctx, skillID, limit)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordUsage journals one explicit fetch (get_skill). Selection journals
// through the same path.
func (s *Service) RecordUsage(ctx context.Context, skillID int64, conversationID *int64, contextText string, matched []string) error {
	return s.recordUsage(ctx, skillID, conversationID, contextText, matched)
}

func (s *Service) recordUsage(ctx context.Context, skillID int64, conversationID *int64, contextText string, matched []string) error {
	now := s.clock.Now().UTC()
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Skills()
		skill, err := repo.GetSkill(ctx, skillID)
		if err != nil {
			return err
		}
		usage := &domainskill.Usage{
			SkillID: skillID, VersionNo: skill.CurrentVersion, ConversationID: conversationID,
			Context: truncateRunes(contextText, 500), MatchedTerms: matched, CreatedAt: now,
		}
		_, err = repo.RecordUsage(ctx, usage)
		return err
	})
}

// SelectSkills returns the active skills relevant to one query, in rank
// order. It is the same selection the assistant context uses, exposed so
// tests and tools ask one question.
func (s *Service) SelectSkills(ctx context.Context, query string, conversationID *int64) ([]selectedSkill, error) {
	skills, err := s.activeSkills(ctx)
	if err != nil {
		return nil, err
	}
	selected := selectSkills(query, skills)
	// Journal the injection: analysis needs to know what was really used.
	for _, item := range selected {
		_ = s.recordUsage(ctx, item.Skill.ID, conversationID, query, item.Matched)
	}
	return selected, nil
}

func (s *Service) activeSkills(ctx context.Context) ([]*domainskill.Skill, error) {
	var out []*domainskill.Skill
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		active := domainskill.StatusActive
		found, _, err := tx.Skills().ListSkills(ctx, Filter{Status: &active, Limit: maxLimit})
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) getSkill(ctx context.Context, id int64) (*domainskill.Skill, error) {
	var found *domainskill.Skill
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		skill, err := tx.Skills().GetSkill(ctx, id)
		if err != nil {
			return err
		}
		found = skill
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// ContextSnapshot is the query-independent part: the catalogue of active
// skills plus the working instructions. It is a hint, not a source of
// truth — the model fetches full instructions through get_skill.
func (s *Service) ContextSnapshot(ctx context.Context) (string, error) {
	skills, err := s.activeSkills(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(skills) > 0 {
		b.WriteString("Навыки LifeOS (активные; полные инструкции подтягиваются инструментом get_skill, когда навык подходит к поручению):\n")
		for i, skill := range skills {
			if i >= catalogLimit {
				break
			}
			fmt.Fprintf(&b, "- «%s»: %s Когда: %s\n",
				truncateRunes(skill.Name, 80),
				truncateRunes(skill.Purpose, 160),
				truncateRunes(skill.WhenToUse, 160))
		}
		if len(skills) > catalogLimit {
			fmt.Fprintf(&b, "(и ещё %d навыков — список через list_skills)\n", len(skills)-catalogLimit)
		}
	}
	if s.working != nil {
		if text, err := s.working.GetWorkingInstructions(ctx); err == nil && strings.TrimSpace(text) != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("Рабочие инструкции ассистента:\n" + truncateRunes(text, 4000))
		}
	}
	return b.String(), nil
}

// SkillsSnapshot is the per-turn part: catalogue plus the full
// instructions of only the skills selected for this query. Each selected
// skill is journalled as used.
func (s *Service) SkillsSnapshot(ctx context.Context, query string, conversationID int64) (string, error) {
	catalog, err := s.ContextSnapshot(ctx)
	if err != nil {
		return "", err
	}
	skills, err := s.activeSkills(ctx)
	if err != nil {
		return "", err
	}
	selected := selectSkills(query, skills)
	var cid *int64
	if conversationID > 0 {
		cid = &conversationID
	}
	for _, item := range selected {
		_ = s.recordUsage(ctx, item.Skill.ID, cid, query, item.Matched)
	}
	if len(selected) == 0 {
		return catalog, nil
	}
	var b strings.Builder
	if catalog != "" {
		b.WriteString(catalog)
		b.WriteString("\n\n")
	}
	b.WriteString("Навыки, подходящие к текущему поручению (подключены автоматически; если пользователь попросил не применять конкретный навык, его инструкции не действуют):\n")
	for _, item := range selected {
		skill := item.Skill
		fmt.Fprintf(&b, "\n## Навык «%s» (версия %d)\nНазначение: %s\nКогда применять: %s\nИнструкции:\n%s\n",
			skill.Name, skill.CurrentVersion, skill.Purpose, skill.WhenToUse, skill.Instructions)
		if strings.TrimSpace(skill.Limitations) != "" {
			fmt.Fprintf(&b, "Ограничения: %s\n", skill.Limitations)
		}
		if strings.TrimSpace(skill.Examples) != "" {
			fmt.Fprintf(&b, "Примеры: %s\n", skill.Examples)
		}
	}
	return b.String(), nil
}

// GetWorkingInstructions returns the stored working instructions block.
func (s *Service) GetWorkingInstructions(ctx context.Context) (string, error) {
	if s.working == nil {
		return "", ErrNoWorkingStore
	}
	return s.working.GetWorkingInstructions(ctx)
}

// SetWorkingInstructions replaces the working instructions block. It is
// the user's direct edit path; assistant changes go through proposals.
func (s *Service) SetWorkingInstructions(ctx context.Context, text string) (string, error) {
	if s.working == nil {
		return "", ErrNoWorkingStore
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > domainskill.MaxInstructionsRunes {
		return "", domainskill.ErrInvalidInstructions
	}
	if err := s.working.SetWorkingInstructions(ctx, text); err != nil {
		return "", err
	}
	s.publish("skill.instructions.updated", map[string]any{})
	return text, nil
}

func normalizeContent(content domainskill.Content) domainskill.Content {
	content.Name = strings.TrimSpace(content.Name)
	content.Purpose = strings.TrimSpace(content.Purpose)
	content.WhenToUse = strings.TrimSpace(content.WhenToUse)
	content.Instructions = strings.TrimSpace(content.Instructions)
	content.Limitations = strings.TrimSpace(content.Limitations)
	content.Examples = strings.TrimSpace(content.Examples)
	content.Tags = domainskill.NormalizeTags(content.Tags)
	return content
}

func sameContent(a, b domainskill.Content) bool {
	return domainskill.NormalizeName(a.Name) == domainskill.NormalizeName(b.Name) &&
		strings.TrimSpace(a.Purpose) == strings.TrimSpace(b.Purpose) &&
		strings.TrimSpace(a.WhenToUse) == strings.TrimSpace(b.WhenToUse) &&
		strings.TrimSpace(a.Instructions) == strings.TrimSpace(b.Instructions) &&
		strings.TrimSpace(a.Limitations) == strings.TrimSpace(b.Limitations) &&
		strings.TrimSpace(a.Examples) == strings.TrimSpace(b.Examples) &&
		strings.Join(domainskill.NormalizeTags(a.Tags), ",") == strings.Join(domainskill.NormalizeTags(b.Tags), ",")
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
