// Package goals implements long-term goal management: the lifecycle of a
// goal, its plan of milestones, links to the rest of LifeOS, factual progress
// tracking and plan revisions that wait for the user's approval.
//
// The service deliberately treats the assistant as a drafter, not a decider:
// a goal created by the model starts in `proposed` and organized execution
// begins only after approval, while significant changes to an approved plan
// become pending proposals instead of silent edits.
package goals

import (
	"context"
	"errors"
	"time"

	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// Errors shared by the service and the transports.
var (
	ErrNotFound           = errors.New("goal not found")
	ErrMilestoneNotFound  = errors.New("goal milestone not found")
	ErrLinkNotFound       = errors.New("goal link not found")
	ErrProposalNotFound   = errors.New("goal proposal not found")
	ErrVersionConflict    = errors.New("goal was changed by another operation")
	ErrInvalidTransition  = errors.New("goal status transition is not allowed")
	ErrInvalidTarget      = errors.New("goal link target does not exist")
	ErrGoalNotProposed    = errors.New("the plan of an approved goal changes only through a proposal")
	ErrGoalTerminal       = errors.New("a finished goal cannot change any more")
	ErrNoPendingProposal  = errors.New("the proposal is not pending anymore")
	ErrAlreadyProposed    = errors.New("a pending proposal already exists for this goal")
	ErrEmptyPayload       = errors.New("the proposal would change nothing")
	ErrMilestoneOutside   = errors.New("the milestone belongs to another goal")
	ErrProposalNotAllowed = errors.New("proposals make sense only for an approved goal")
)

// Clock supplies timestamps to all mutations.
type Clock interface{ Now() time.Time }

// Filter bounds goal listing.
type Filter struct {
	Query  string
	Status *domaingoal.Status
	Limit  int
	Offset int
}

// CreateInput is one drafted goal with its plan.
type CreateInput struct {
	Title       string
	Description string
	TargetDate  *time.Time
	Milestones  []domaingoal.MilestoneInput
	Source      *domaingoal.TargetType
	SourceID    *int64
}

// UpdateInput is a partial, optimistic-locked edit of the goal itself.
type UpdateInput struct {
	Title       *string
	Description *string
	TargetDate  *time.Time
	ClearTarget bool
}

// UpdatePlanInput is a direct plan edit allowed only while the goal is still
// awaiting approval.
type UpdatePlanInput struct {
	Title         *string
	Description   *string
	TargetDate    *time.Time
	ClearTarget   bool
	AddMilestones []domaingoal.MilestoneInput
	RemoveIDs     []int64
}

// MilestoneStatusInput updates the state of one milestone.
type MilestoneStatusInput struct {
	MilestoneID int64
	Status      domaingoal.MilestoneStatus
}

// MilestoneUpdateInput is a partial, optimistic-locked edit of one milestone.
type MilestoneUpdateInput struct {
	Title    *string
	Criteria *string
	Status   *domaingoal.MilestoneStatus
	DueDate  *time.Time
	ClearDue bool
}

// LinkInput connects a goal to another LifeOS object.
type LinkInput struct {
	TargetType domaingoal.TargetType
	TargetID   int64
	Relation   string
}

// RecordInput is one observed piece of progress.
type RecordInput struct {
	Summary    string
	SourceType *domaingoal.TargetType
	SourceID   *int64
}

// TaskView is the slice of task data the progress calculation needs.
type TaskView struct {
	ID          int64
	Title       string
	Status      string
	CompletedAt *time.Time
}

// TaskProvider fetches linked tasks for progress and stall detection.
type TaskProvider interface {
	GetTask(ctx context.Context, id int64) (TaskView, error)
}

// Repository is the persistence boundary of the goal aggregate.
type Repository interface {
	InsertGoal(ctx context.Context, goal *domaingoal.Goal, milestones []domaingoal.MilestoneInput) (int64, error)
	GetGoal(ctx context.Context, id int64) (*domaingoal.Goal, error)
	UpdateGoal(ctx context.Context, id int64, expectedVersion int, goal *domaingoal.Goal) (*domaingoal.Goal, error)
	DeleteGoal(ctx context.Context, id int64) error
	ListGoals(ctx context.Context, filter Filter) ([]*domaingoal.Goal, int, error)
	CountByStatus(ctx context.Context) (map[domaingoal.Status]int, error)

	InsertMilestone(ctx context.Context, milestone *domaingoal.Milestone) (int64, error)
	ListMilestones(ctx context.Context, goalID int64) ([]domaingoal.Milestone, error)
	GetMilestone(ctx context.Context, goalID, milestoneID int64) (*domaingoal.Milestone, error)
	UpdateMilestone(ctx context.Context, milestoneID int64, expectedVersion int, milestone *domaingoal.Milestone) (*domaingoal.Milestone, error)
	DeleteMilestone(ctx context.Context, goalID, milestoneID int64) error

	InsertLink(ctx context.Context, link *domaingoal.Link) (int64, error)
	ListLinks(ctx context.Context, goalID int64) ([]domaingoal.Link, error)
	// ListLinksByTarget returns every goal linked to one LifeOS object. It exists
	// so completing a task finds all its goals in one query instead of walking a
	// bounded goal page and missing links past the limit.
	ListLinksByTarget(ctx context.Context, target domaingoal.TargetType, targetID int64) ([]domaingoal.Link, error)
	DeleteLink(ctx context.Context, goalID, linkID int64) error
	// TargetExists verifies a link target inside the same transaction, mirroring
	// the memory repository: a second connection would deadlock on a
	// single-writer SQLite.
	TargetExists(ctx context.Context, target domaingoal.TargetType, id int64) (bool, error)

	InsertProgress(ctx context.Context, event *domaingoal.ProgressEvent) (int64, error)
	ListProgress(ctx context.Context, goalID int64, limit int) ([]domaingoal.ProgressEvent, error)
	LastActivity(ctx context.Context, goalID int64) (*time.Time, error)
	// HasProgressSource reports whether the journal already holds an entry of
	// this kind for the given source. Progress recording is idempotent because
	// the same completion may be observed by the live pump and by the catch-up
	// reconciliation.
	HasProgressSource(ctx context.Context, goalID int64, kind domaingoal.ProgressKind, sourceType domaingoal.TargetType, sourceID int64) (bool, error)

	InsertProposal(ctx context.Context, proposal *domaingoal.Proposal) (int64, error)
	GetProposal(ctx context.Context, goalID, proposalID int64) (*domaingoal.Proposal, error)
	ListProposals(ctx context.Context, goalID int64, pendingOnly bool) ([]domaingoal.Proposal, error)
	SetProposalStatus(ctx context.Context, proposalID int64, status domaingoal.ProposalStatus, at time.Time) error
	HasPendingProposal(ctx context.Context, goalID int64) (bool, error)
}

// Transaction exposes one transactional goal repository.
type Transaction interface{ Goals() Repository }

// UnitOfWork is the goal transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(context.Context, Transaction) error) error
}

// Progress is the factual state of a goal: what the milestones say and what
// the linked work has actually produced.
type Progress struct {
	MilestonesTotal     int
	MilestonesDone      int
	MilestonesCompleted int
	LinkedTasks         int
	LinkedTasksDone     int
	Percent             *int
	LastActivityAt      *time.Time
}

// Signals lists concrete deviations worth attention. Every signal names its
// reason, so the assistant and the user argue from the same facts.
type Signals struct {
	Stalled         bool
	StallReason     string
	Overdue         bool
	NoOpenWork      bool
	PendingProposal bool
}

// Review is the computed state one goal is in right now.
type Review struct {
	Progress Progress
	Signals  Signals
}

// View is one goal with everything the UI and the assistant need.
type View struct {
	Goal        *domaingoal.Goal
	Milestones  []domaingoal.Milestone
	Links       []domaingoal.Link
	Proposals   []domaingoal.Proposal
	ProgressLog []domaingoal.ProgressEvent
	Review      *Review
}

// Stats is the compact state used by the workspace and the assistant.
type Stats struct {
	Proposed  int
	Active    int
	Paused    int
	Achieved  int
	Abandoned int
}

// StalledGoal pairs one active goal with the reason it stopped moving.
type StalledGoal struct {
	Goal   *domaingoal.Goal
	Review *Review
}
