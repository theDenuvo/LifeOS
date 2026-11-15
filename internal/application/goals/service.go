package goals

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
	"github.com/theDenuvo/LifeOS/internal/events"
)

const (
	defaultLimit     = 30
	maxLimit         = 100
	progressLogLimit = 50
	// stallAfterDays is how long an active goal may stay without any recorded
	// progress before it counts as stopped. A week proved noisy for plans that
	// legitimately run in monthly steps; two weeks ask a real question.
	stallAfterDays = 14
)

// Service coordinates goal mutations, approval, plan proposals and progress.
type Service struct {
	uow    UnitOfWork
	tasks  TaskProvider
	clock  Clock
	broker events.Publisher
}

// Option customises the service.
type Option func(*Service)

// WithTaskProvider attaches the task engine used to compute factual progress.
func WithTaskProvider(provider TaskProvider) Option { return func(s *Service) { s.tasks = provider } }

// WithPublisher publishes goal changes to the event stream.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) { s.broker = publisher }
}

// NewService builds the goal service.
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

// Create stores a goal in the `proposed` state: the plan exists, but organized
// work starts only after the user approves it.
func (s *Service) Create(ctx context.Context, input CreateInput) (*View, error) {
	now := s.clock.Now().UTC()
	g := &domaingoal.Goal{
		Title:       strings.TrimSpace(input.Title),
		Description: strings.TrimSpace(input.Description),
		Status:      domaingoal.StatusProposed,
		TargetDate:  input.TargetDate,
		CreatedAt:   now,
		UpdatedAt:   now,
		Version:     1,
	}
	for _, milestone := range input.Milestones {
		if err := domaingoal.ValidateMilestoneInput(milestone); err != nil {
			return nil, err
		}
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}

	var id int64
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Goals().InsertGoal(ctx, g, input.Milestones)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, id, domaingoal.ProgressGoalCreated, describeCreate(input), input.Source, input.SourceID, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.created", id)
	return s.Get(ctx, id)
}

// List returns goals with their milestone lists and computed progress.
func (s *Service) List(ctx context.Context, filter Filter) ([]*View, int, error) {
	filter.Limit = clamp(filter.Limit)
	var goals []*domaingoal.Goal
	var total int
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		goals, total, err = tx.Goals().ListGoals(ctx, filter)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	views := make([]*View, 0, len(goals))
	// Progress is computed after the read transaction closes, so task lookups
	// never contend with the single SQLite connection.
	for _, g := range goals {
		view, err := s.Get(ctx, g.ID)
		if err != nil {
			return nil, 0, err
		}
		views = append(views, view)
	}
	return views, total, nil
}

// aggregate is everything read from one goal transaction. It deliberately
// stops short of the computed progress: progress needs the task engine, and
// asking it inside the transaction would wait on the single SQLite connection
// the transaction already holds.
type aggregate struct {
	goal       *domaingoal.Goal
	milestones []domaingoal.Milestone
	links      []domaingoal.Link
	proposals  []domaingoal.Proposal
	log        []domaingoal.ProgressEvent
	lastActive *time.Time
}

// cluster reads the aggregate of one goal inside an open transaction.
func (s *Service) cluster(ctx context.Context, tx Transaction, id int64) (aggregate, error) {
	repo := tx.Goals()
	g, err := repo.GetGoal(ctx, id)
	if err != nil {
		return aggregate{}, err
	}
	milestones, err := repo.ListMilestones(ctx, id)
	if err != nil {
		return aggregate{}, err
	}
	sort.Slice(milestones, func(i, j int) bool { return milestones[i].Position < milestones[j].Position })
	links, err := repo.ListLinks(ctx, id)
	if err != nil {
		return aggregate{}, err
	}
	proposals, err := repo.ListProposals(ctx, id, false)
	if err != nil {
		return aggregate{}, err
	}
	log, err := repo.ListProgress(ctx, id, progressLogLimit)
	if err != nil {
		return aggregate{}, err
	}
	last, err := repo.LastActivity(ctx, id)
	if err != nil {
		return aggregate{}, err
	}
	return aggregate{
		goal: g, milestones: milestones, links: links,
		proposals: proposals, log: log, lastActive: last,
	}, nil
}

// buildView turns a transaction aggregate into the full view. Task lookups and
// the progress arithmetic happen here, outside the goal transaction.
func (s *Service) buildView(ctx context.Context, data aggregate) (*View, error) {
	progress := s.computeProgress(ctx, data.goal.ID, data.milestones, data.links, data.lastActive)
	review := s.review(data.goal, progress)
	for _, proposal := range data.proposals {
		if proposal.Status == domaingoal.ProposalPending {
			review.Signals.PendingProposal = true
			break
		}
	}
	return &View{
		Goal:        data.goal,
		Milestones:  data.milestones,
		Links:       data.links,
		Proposals:   data.proposals,
		ProgressLog: data.log,
		Review:      review,
	}, nil
}

// Get returns one goal with milestones, links, pending proposals, the progress
// journal and the computed review.
func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	var data aggregate
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		data, err = s.cluster(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.buildView(ctx, data)
}

// Update applies a user edit of the goal itself. Editing the title, note or
// target date never changes the plan, so it is allowed in any non-terminal
// status.
func (s *Service) Update(ctx context.Context, id int64, expectedVersion int, input UpdateInput) (*View, error) {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		g, err := tx.Goals().GetGoal(ctx, id)
		if err != nil {
			return err
		}
		if g.Version != expectedVersion {
			return ErrVersionConflict
		}
		if g.Status.Terminal() {
			return ErrInvalidTransition
		}
		applyGoalEdit(g, input)
		if err := g.Validate(); err != nil {
			return err
		}
		_, err = tx.Goals().UpdateGoal(ctx, id, expectedVersion, g)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", id)
	return s.Get(ctx, id)
}

// Delete removes a goal with its plan, links, proposals and journal.
func (s *Service) Delete(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Goals().DeleteGoal(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("goal.deleted", id)
	return nil
}

// Approve moves a proposed goal into organized execution. This is the user's
// decision; the assistant reaches it through a confirmation-gated tool.
func (s *Service) Approve(ctx context.Context, id int64) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, id)
		if err != nil {
			return err
		}
		if !domaingoal.CanTransition(g.Status, domaingoal.StatusActive) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, g.Status, domaingoal.StatusActive)
		}
		g.Status = domaingoal.StatusActive
		g.ApprovedAt = &now
		g.UpdatedAt = now
		updated, err := repo.UpdateGoal(ctx, id, g.Version, g)
		if err != nil {
			return err
		}
		g = updated
		return s.record(ctx, tx, id, domaingoal.ProgressApproved, "Цель утверждена, работа начата", nil, nil, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", id)
	return s.Get(ctx, id)
}

// SetStatus applies pause, resume, achieve and abandon transitions.
func (s *Service) SetStatus(ctx context.Context, id int64, status domaingoal.Status) (*View, error) {
	if !status.IsValid() {
		return nil, domaingoal.ErrInvalidStatus
	}
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, id)
		if err != nil {
			return err
		}
		if !domaingoal.CanTransition(g.Status, status) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, g.Status, status)
		}
		g.Status = status
		g.UpdatedAt = now
		if status == domaingoal.StatusActive && g.ApprovedAt == nil {
			g.ApprovedAt = &now
		}
		if _, err := repo.UpdateGoal(ctx, id, g.Version, g); err != nil {
			return err
		}
		return s.record(ctx, tx, id, domaingoal.ProgressStatus, "Статус цели: "+string(status), nil, nil, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", id)
	return s.Get(ctx, id)
}

// AddMilestone extends the plan. An approved plan is changed only through a
// proposal; a proposed plan may still be edited freely.
func (s *Service) AddMilestone(ctx context.Context, goalID int64, input domaingoal.MilestoneInput) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if err := domaingoal.ValidateMilestoneInput(input); err != nil {
			return err
		}
		// A finished goal is history: its plan is immutable. A goal being worked
		// on extends its plan only through a proposal.
		if g.Status.Terminal() {
			return ErrGoalTerminal
		}
		if g.Status.Active() {
			return ErrGoalNotProposed
		}
		milestone := &domaingoal.Milestone{
			GoalID: goalID, Title: strings.TrimSpace(input.Title),
			Criteria: strings.TrimSpace(input.Criteria), DueDate: input.DueDate,
			Status: domaingoal.MilestonePending, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		_, err = repo.InsertMilestone(ctx, milestone)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// UpdateMilestone edits one step of the plan or moves its state.
//
// Two different things live behind one call and must not be confused: a status
// move is factual progress and may happen at any time, while editing the
// step's wording, criteria or deadline is a plan change. For an approved goal
// the latter goes through propose_goal_change, and a finished goal accepts
// neither.
func (s *Service) UpdateMilestone(ctx context.Context, goalID, milestoneID int64, expectedVersion int, input MilestoneUpdateInput) (*View, error) {
	editsPlan := input.Title != nil || input.Criteria != nil || input.DueDate != nil || input.ClearDue
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if g.Status.Terminal() {
			return ErrGoalTerminal
		}
		if editsPlan && g.Status.Active() {
			return ErrGoalNotProposed
		}
		m, err := repo.GetMilestone(ctx, goalID, milestoneID)
		if err != nil {
			return err
		}
		if m.Version != expectedVersion {
			return ErrVersionConflict
		}
		if input.Title != nil {
			m.Title = strings.TrimSpace(*input.Title)
		}
		if input.Criteria != nil {
			m.Criteria = strings.TrimSpace(*input.Criteria)
		}
		if input.DueDate != nil {
			m.DueDate = input.DueDate
		}
		if input.ClearDue {
			m.DueDate = nil
		}
		var statusChanged bool
		if input.Status != nil && *input.Status != m.Status {
			m.Status = *input.Status
			statusChanged = true
			if m.Status.Completed() && m.CompletedAt == nil {
				completed := now
				m.CompletedAt = &completed
			}
			if !m.Status.Completed() {
				m.CompletedAt = nil
			}
		}
		if err := m.Validate(); err != nil {
			return err
		}
		if _, err := repo.UpdateMilestone(ctx, milestoneID, expectedVersion, m); err != nil {
			return err
		}
		if statusChanged {
			return s.record(ctx, tx, goalID, domaingoal.ProgressMilestone,
				fmt.Sprintf("Этап «%s» → %s", m.Title, m.Status), nil, nil, now)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// RemoveMilestone deletes one step of the plan; the same proposal rule as
// AddMilestone applies.
func (s *Service) RemoveMilestone(ctx context.Context, goalID, milestoneID int64) (*View, error) {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if g.Status.Terminal() {
			return ErrGoalTerminal
		}
		if g.Status.Active() {
			return ErrGoalNotProposed
		}
		return repo.DeleteMilestone(ctx, goalID, milestoneID)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// UpdatePlan rewrites the plan of a goal that is still awaiting approval.
func (s *Service) UpdatePlan(ctx context.Context, goalID int64, expectedVersion int, input UpdatePlanInput) (*View, error) {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if g.Version != expectedVersion {
			return ErrVersionConflict
		}
		if g.Status != domaingoal.StatusProposed {
			return ErrGoalNotProposed
		}
		applyGoalEdit(g, UpdateInput{
			Title: input.Title, Description: input.Description,
			TargetDate: input.TargetDate, ClearTarget: input.ClearTarget,
		})
		if err := g.Validate(); err != nil {
			return err
		}
		if _, err := repo.UpdateGoal(ctx, goalID, expectedVersion, g); err != nil {
			return err
		}
		for _, id := range input.RemoveIDs {
			if err := repo.DeleteMilestone(ctx, goalID, id); err != nil {
				return err
			}
		}
		now := s.clock.Now().UTC()
		for _, ms := range input.AddMilestones {
			if err := domaingoal.ValidateMilestoneInput(ms); err != nil {
				return err
			}
			milestone := &domaingoal.Milestone{
				GoalID: goalID, Title: strings.TrimSpace(ms.Title),
				Criteria: strings.TrimSpace(ms.Criteria), DueDate: ms.DueDate,
				Status: domaingoal.MilestonePending, CreatedAt: now, UpdatedAt: now, Version: 1,
			}
			if _, err := repo.InsertMilestone(ctx, milestone); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// ProposeChange records a structured plan revision for user approval. Only
// approved goals need this gate; everything else edits the plan directly.
func (s *Service) ProposeChange(ctx context.Context, goalID int64, payload domaingoal.ProposalPayload, summary string) (*domaingoal.Proposal, error) {
	proposal := &domaingoal.Proposal{
		GoalID: goalID, Summary: strings.TrimSpace(summary),
		Payload: payload, Status: domaingoal.ProposalPending,
		CreatedAt: s.clock.Now().UTC(),
	}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if !g.Status.Active() {
			return ErrProposalNotAllowed
		}
		if payload.Empty() {
			return ErrEmptyPayload
		}
		if err := proposal.Validate(); err != nil {
			return err
		}
		pending, err := repo.HasPendingProposal(ctx, goalID)
		if err != nil {
			return err
		}
		if pending {
			return ErrAlreadyProposed
		}
		if err := s.validatePayloadTargets(ctx, tx, goalID, payload); err != nil {
			return err
		}
		proposal.ID, err = repo.InsertProposal(ctx, proposal)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return proposal, nil
}

// validatePayloadTargets checks that milestone ids referenced by the payload
// really belong to the goal, before the user is asked to approve anything.
func (s *Service) validatePayloadTargets(ctx context.Context, tx Transaction, goalID int64, payload domaingoal.ProposalPayload) error {
	if len(payload.CompleteMilestoneIDs) == 0 && len(payload.SkipMilestoneIDs) == 0 &&
		len(payload.ReopenMilestoneIDs) == 0 && len(payload.RemoveMilestoneIDs) == 0 {
		return nil
	}
	milestones, err := tx.Goals().ListMilestones(ctx, goalID)
	if err != nil {
		return err
	}
	byID := make(map[int64]struct{}, len(milestones))
	for _, m := range milestones {
		byID[m.ID] = struct{}{}
	}
	for _, ids := range [][]int64{payload.CompleteMilestoneIDs, payload.SkipMilestoneIDs, payload.ReopenMilestoneIDs, payload.RemoveMilestoneIDs} {
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return ErrMilestoneOutside
			}
		}
	}
	return nil
}

// ApplyProposal applies an approved plan revision inside one transaction. The
// user's approval is recorded in the journal.
func (s *Service) ApplyProposal(ctx context.Context, goalID, proposalID int64) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		proposal, err := repo.GetProposal(ctx, goalID, proposalID)
		if err != nil {
			return err
		}
		if proposal.Status != domaingoal.ProposalPending {
			return ErrNoPendingProposal
		}
		g, err := repo.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		if !g.Status.Active() {
			return ErrProposalNotAllowed
		}
		payload := proposal.Payload
		if payload.Title != nil {
			g.Title = strings.TrimSpace(*payload.Title)
		}
		if payload.Description != nil {
			g.Description = strings.TrimSpace(*payload.Description)
		}
		if payload.ClearTargetDate {
			g.TargetDate = nil
		} else if payload.TargetDate != nil {
			g.TargetDate = payload.TargetDate
		}
		if err := g.Validate(); err != nil {
			return err
		}
		if _, err := repo.UpdateGoal(ctx, goalID, g.Version, g); err != nil {
			return err
		}
		milestones, err := repo.ListMilestones(ctx, goalID)
		if err != nil {
			return err
		}
		byID := make(map[int64]domaingoal.Milestone, len(milestones))
		for _, m := range milestones {
			byID[m.ID] = m
		}
		for _, id := range payload.CompleteMilestoneIDs {
			if err := applyMilestoneStatus(ctx, repo, byID, goalID, id, domaingoal.MilestoneDone, now); err != nil {
				return err
			}
		}
		for _, id := range payload.SkipMilestoneIDs {
			if err := applyMilestoneStatus(ctx, repo, byID, goalID, id, domaingoal.MilestoneSkipped, now); err != nil {
				return err
			}
		}
		for _, id := range payload.ReopenMilestoneIDs {
			if err := applyMilestoneStatus(ctx, repo, byID, goalID, id, domaingoal.MilestonePending, now); err != nil {
				return err
			}
		}
		for _, id := range payload.RemoveMilestoneIDs {
			if err := repo.DeleteMilestone(ctx, goalID, id); err != nil {
				return err
			}
		}
		for _, ms := range payload.AddMilestones {
			if err := domaingoal.ValidateMilestoneInput(ms); err != nil {
				return err
			}
			milestone := &domaingoal.Milestone{
				GoalID: goalID, Title: strings.TrimSpace(ms.Title),
				Criteria: strings.TrimSpace(ms.Criteria), DueDate: ms.DueDate,
				Status: domaingoal.MilestonePending, CreatedAt: now, UpdatedAt: now, Version: 1,
			}
			if _, err := repo.InsertMilestone(ctx, milestone); err != nil {
				return err
			}
		}
		if err := repo.SetProposalStatus(ctx, proposalID, domaingoal.ProposalApplied, now); err != nil {
			return err
		}
		return s.record(ctx, tx, goalID, domaingoal.ProgressProposal,
			"Изменение плана применено: "+proposal.Summary, nil, nil, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// RejectProposal declines a plan revision without touching the goal.
func (s *Service) RejectProposal(ctx context.Context, goalID, proposalID int64) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		proposal, err := tx.Goals().GetProposal(ctx, goalID, proposalID)
		if err != nil {
			return err
		}
		if proposal.Status != domaingoal.ProposalPending {
			return ErrNoPendingProposal
		}
		return tx.Goals().SetProposalStatus(ctx, proposalID, domaingoal.ProposalRejected, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// AddLink connects a goal to another LifeOS object.
func (s *Service) AddLink(ctx context.Context, goalID int64, input LinkInput) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		if _, err := repo.GetGoal(ctx, goalID); err != nil {
			return err
		}
		link := &domaingoal.Link{
			GoalID: goalID, TargetType: input.TargetType, TargetID: input.TargetID,
			Relation: strings.TrimSpace(input.Relation), CreatedAt: now,
		}
		if err := link.Validate(); err != nil {
			return err
		}
		exists, err := repo.TargetExists(ctx, link.TargetType, link.TargetID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrInvalidTarget
		}
		_, err = repo.InsertLink(ctx, link)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// RemoveLink disconnects a goal from another LifeOS object.
func (s *Service) RemoveLink(ctx context.Context, goalID, linkID int64) (*View, error) {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Goals().DeleteLink(ctx, goalID, linkID)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// Record appends one observed piece of progress to the journal.
func (s *Service) Record(ctx context.Context, goalID int64, input RecordInput) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Goals()
		if _, err := repo.GetGoal(ctx, goalID); err != nil {
			return err
		}
		// A progress entry claims "this happened in that object", so the object
		// must exist. The check runs inside the same transaction as the insert,
		// exactly like AddLink, so the claim cannot outlive its source.
		if input.SourceType != nil || input.SourceID != nil {
			if input.SourceType == nil || input.SourceID == nil || !input.SourceType.IsValid() || *input.SourceID <= 0 {
				return ErrInvalidTarget
			}
			exists, err := repo.TargetExists(ctx, *input.SourceType, *input.SourceID)
			if err != nil {
				return err
			}
			if !exists {
				return ErrInvalidTarget
			}
		}
		return s.record(ctx, tx, goalID, domaingoal.ProgressNote, input.Summary, input.SourceType, input.SourceID, now)
	})
	if err != nil {
		return nil, err
	}
	s.publish("goal.updated", goalID)
	return s.Get(ctx, goalID)
}

// record appends one journal entry inside an open transaction.
func (s *Service) record(ctx context.Context, tx Transaction, goalID int64, kind domaingoal.ProgressKind, summary string, sourceType *domaingoal.TargetType, sourceID *int64, now time.Time) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = string(kind)
	}
	if !utf8.ValidString(summary) {
		return domaingoal.ErrInvalidProgress
	}
	if len([]rune(summary)) > 1000 {
		summary = string([]rune(summary)[:997]) + "…"
	}
	event := &domaingoal.ProgressEvent{
		GoalID: goalID, Kind: kind, Summary: summary,
		SourceType: sourceType, SourceID: sourceID, RecordedAt: now,
	}
	if _, err := tx.Goals().InsertProgress(ctx, event); err != nil {
		return err
	}
	return nil
}

// OnTaskCompleted records factual progress for every goal the task is linked
// to. It is called from the event pump, so a failure here must never fail the
// task completion that caused it — but the failure is logged and the catch-up
// reconciliation will retry it, because progress must survive a missed event.
func (s *Service) OnTaskCompleted(ctx context.Context, taskID int64) {
	// Resolve the task title before opening the transaction: the task engine
	// reads through the same single-connection pool.
	summary := s.taskSummary(ctx, taskID, "completed")
	var recorded int
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		links, err := tx.Goals().ListLinksByTarget(ctx, domaingoal.TargetTask, taskID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		for _, link := range links {
			recorded += s.recordTaskProgress(ctx, tx, link.GoalID, taskID, summary, now)
		}
		return nil
	})
	if err != nil {
		slog.Warn("goal progress for a completed task failed; the catch-up reconciliation will retry",
			"task_id", taskID, "error", err)
		return
	}
	if recorded > 0 {
		slog.Debug("goal progress recorded", "task_id", taskID, "goals", recorded)
	}
}

// recordTaskProgress appends one task_done journal entry unless the same
// completion is already recorded. It returns 1 when a row was written, so the
// caller knows whether the goal changed.
func (s *Service) recordTaskProgress(ctx context.Context, tx Transaction, goalID, taskID int64, summary string, now time.Time) int {
	repo := tx.Goals()
	exists, err := repo.HasProgressSource(ctx, goalID, domaingoal.ProgressTaskDone, domaingoal.TargetTask, taskID)
	if err != nil {
		slog.Warn("check goal progress source failed", "goal_id", goalID, "task_id", taskID, "error", err)
		return 0
	}
	if exists {
		return 0
	}
	sourceType := domaingoal.TargetTask
	sourceID := taskID
	if err := s.record(ctx, tx, goalID, domaingoal.ProgressTaskDone, summary, &sourceType, &sourceID, now); err != nil {
		slog.Warn("record goal progress failed", "goal_id", goalID, "task_id", taskID, "error", err)
		return 0
	}
	return 1
}

// taskSummary describes a linked task in the journal, falling back to a
// generic sentence when the task cannot be read.
func (s *Service) taskSummary(ctx context.Context, taskID int64, _ string) string {
	if s.tasks != nil {
		if task, err := s.tasks.GetTask(ctx, taskID); err == nil && strings.TrimSpace(task.Title) != "" {
			return fmt.Sprintf("Задача «%s» выполнена", task.Title)
		}
	}
	return fmt.Sprintf("Связанная задача #%d выполнена", taskID)
}

// ReconcileTaskProgress is the catch-up path for the live event pump. A
// completion can be missed when LifeOS was down, when the broker dropped the
// event for a slow subscriber, or when a write failed; this walks every task
// link of every goal and records the missing journal entries. It is
// idempotent, so running it on a schedule costs nothing when everything is
// already recorded. It returns how many entries it added.
func (s *Service) ReconcileTaskProgress(ctx context.Context) (int, error) {
	if s.tasks == nil {
		return 0, nil
	}
	// Collect every task link first, outside any transaction.
	var links []domaingoal.Link
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		links, err = tx.Goals().ListLinksByTarget(ctx, domaingoal.TargetTask, 0)
		return err
	})
	if err != nil {
		return 0, err
	}

	// Group completed tasks by id so the task engine is asked once per task.
	completed := make(map[int64]string)
	for _, link := range links {
		if _, seen := completed[link.TargetID]; seen {
			continue
		}
		task, err := s.tasks.GetTask(ctx, link.TargetID)
		if err != nil || task.Status != "completed" {
			continue
		}
		title := strings.TrimSpace(task.Title)
		if title == "" {
			completed[link.TargetID] = fmt.Sprintf("Связанная задача #%d выполнена", link.TargetID)
		} else {
			completed[link.TargetID] = fmt.Sprintf("Задача «%s» выполнена", title)
		}
	}
	if len(completed) == 0 {
		return 0, nil
	}

	added := 0
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		now := s.clock.Now().UTC()
		for _, link := range links {
			summary, ok := completed[link.TargetID]
			if !ok {
				continue
			}
			added += s.recordTaskProgress(ctx, tx, link.GoalID, link.TargetID, summary, now)
		}
		return nil
	})
	if err != nil {
		return added, err
	}
	if added > 0 {
		slog.Info("goal progress reconciled", "entries", added)
	}
	return added, nil
}

// Stats returns goal counts per status for the workspace.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		counts, err := tx.Goals().CountByStatus(ctx)
		if err != nil {
			return err
		}
		stats = Stats{
			Proposed:  counts[domaingoal.StatusProposed],
			Active:    counts[domaingoal.StatusActive],
			Paused:    counts[domaingoal.StatusPaused],
			Achieved:  counts[domaingoal.StatusAchieved],
			Abandoned: counts[domaingoal.StatusAbandoned],
		}
		return nil
	})
	return stats, err
}

// Stalled lists active goals that stopped moving, with the factual reason.
func (s *Service) Stalled(ctx context.Context) ([]StalledGoal, error) {
	views, _, err := s.List(ctx, Filter{Status: statusPtr(domaingoal.StatusActive), Limit: maxLimit})
	if err != nil {
		return nil, err
	}
	stalled := make([]StalledGoal, 0, len(views))
	for _, view := range views {
		if view.Review != nil && view.Review.Signals.Stalled {
			stalled = append(stalled, StalledGoal{Goal: view.Goal, Review: view.Review})
		}
	}
	return stalled, nil
}

// computeProgress derives the factual progress from milestones and linked
// tasks. It runs outside the goal transaction on purpose: s.tasks reads from
// the same single-connection SQLite pool, so a lookup inside an open
// transaction would wait for the connection the transaction itself holds.
func (s *Service) computeProgress(ctx context.Context, goalID int64, milestones []domaingoal.Milestone, links []domaingoal.Link, lastActive *time.Time) Progress {
	progress := Progress{MilestonesTotal: len(milestones), LastActivityAt: lastActive}
	for _, m := range milestones {
		// Skipped steps close the plan but are not counted as achieved steps.
		if m.Status.Done() {
			progress.MilestonesDone++
		}
		if m.Status.Completed() {
			progress.MilestonesCompleted++
		}
	}
	if s.tasks != nil {
		for _, link := range links {
			if link.TargetType != domaingoal.TargetTask {
				continue
			}
			progress.LinkedTasks++
			if task, err := s.tasks.GetTask(ctx, link.TargetID); err == nil && task.Status == "completed" {
				progress.LinkedTasksDone++
			}
		}
	}
	switch {
	case progress.MilestonesTotal > 0:
		percent := progress.MilestonesDone * 100 / progress.MilestonesTotal
		progress.Percent = &percent
	case progress.LinkedTasks > 0:
		percent := progress.LinkedTasksDone * 100 / progress.LinkedTasks
		progress.Percent = &percent
	}
	return progress
}

// review turns facts into named deviations. Every signal explains itself so
// the user can judge whether it deserves attention.
func (s *Service) review(g *domaingoal.Goal, progress Progress) *Review {
	review := &Review{Progress: progress}
	now := s.clock.Now().UTC()
	// Only a goal that is actually being worked on can "stall". A proposed goal
	// is waiting for the user, and a paused one was stopped on purpose, so
	// neither belongs in the deviation stream.
	if g.Status == domaingoal.StatusActive {
		last := progress.LastActivityAt
		if last == nil {
			last = &g.CreatedAt
		}
		if silent := now.Sub(*last); silent > stallAfterDays*24*time.Hour {
			review.Signals.Stalled = true
			review.Signals.StallReason = fmt.Sprintf("нет записей о прогрессе %d дней", int(silent.Hours()/24))
		}
		if g.TargetDate != nil && g.TargetDate.Before(now) {
			review.Signals.Overdue = true
		}
	}
	if progress.MilestonesTotal > 0 && progress.MilestonesCompleted == progress.MilestonesTotal {
		// everything closed (done or skipped) but not marked achieved — the user decides
		review.Signals.NoOpenWork = true
	} else if progress.MilestonesTotal == 0 && progress.LinkedTasks > 0 && progress.LinkedTasksDone == progress.LinkedTasks {
		review.Signals.NoOpenWork = true
	}
	return review
}

// applyMilestoneStatus moves one milestone inside a proposal application.
func applyMilestoneStatus(ctx context.Context, repo Repository, byID map[int64]domaingoal.Milestone, goalID, id int64, status domaingoal.MilestoneStatus, now time.Time) error {
	m, ok := byID[id]
	if !ok || m.GoalID != goalID {
		return ErrMilestoneOutside
	}
	m.Status = status
	m.UpdatedAt = now
	if status.Done() && m.CompletedAt == nil {
		m.CompletedAt = &now
	}
	if !status.Done() {
		m.CompletedAt = nil
	}
	_, err := repo.UpdateMilestone(ctx, id, m.Version, &m)
	return err
}

func statusPtr(status domaingoal.Status) *domaingoal.Status { return &status }

func clamp(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// applyGoalEdit applies a partial edit in place.
func applyGoalEdit(g *domaingoal.Goal, input UpdateInput) {
	if input.Title != nil {
		g.Title = strings.TrimSpace(*input.Title)
	}
	if input.Description != nil {
		g.Description = strings.TrimSpace(*input.Description)
	}
	if input.ClearTarget {
		g.TargetDate = nil
	} else if input.TargetDate != nil {
		g.TargetDate = input.TargetDate
	}
}

func describeCreate(input CreateInput) string {
	count := len(input.Milestones)
	switch count {
	case 0:
		return "Цель поставлена, план ещё не составлен"
	case 1:
		return "Цель поставлена, план из 1 этапа ожидает утверждения"
	default:
		return fmt.Sprintf("Цель поставлена, план из %d этапов ожидает утверждения", count)
	}
}

// publish notifies the event stream; failures never fail the mutation.
func (s *Service) publish(eventType string, goalID int64) {
	if s.broker == nil {
		return
	}
	s.broker.Publish(eventType, map[string]any{"id": goalID})
}
