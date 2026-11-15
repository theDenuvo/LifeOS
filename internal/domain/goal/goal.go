// Package goal owns the model of long-term goals: their lifecycle, the plan
// that breaks them into milestones, links to the rest of LifeOS, progress
// evidence and proposed plan changes that wait for user approval.
//
// The product rule behind the lifecycle: the assistant may draft a goal and a
// plan, but organized execution starts only after the user approves the goal,
// and significant plan changes are agreed with the user instead of applied
// silently.
package goal

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is the lifecycle of a goal.
//
// proposed — a drafted goal with a plan that still needs user approval;
// active — approved and being worked on; paused — deliberately stopped;
// achieved — reached; abandoned — given up. Terminal states are kept for
// history rather than deleted.
type Status string

const (
	StatusProposed  Status = "proposed"
	StatusActive    Status = "active"
	StatusPaused    Status = "paused"
	StatusAchieved  Status = "achieved"
	StatusAbandoned Status = "abandoned"
)

// IsValid reports whether the status is known.
func (s Status) IsValid() bool {
	switch s {
	case StatusProposed, StatusActive, StatusPaused, StatusAchieved, StatusAbandoned:
		return true
	default:
		return false
	}
}

// Terminal reports whether the goal is finished and can no longer change its
// plan. History and progress stay readable.
func (s Status) Terminal() bool {
	return s == StatusAchieved || s == StatusAbandoned
}

// Active reports whether organized execution is currently expected from the
// goal.
func (s Status) Active() bool {
	return s == StatusActive || s == StatusPaused
}

// transitions lists the lifecycle moves the product rules allow. Anything not
// in this map is refused instead of silently applied.
var transitions = map[Status]map[Status]struct{}{
	StatusProposed: {
		StatusActive:    {}, // user approved the goal and its plan
		StatusAbandoned: {},
	},
	StatusActive: {
		StatusPaused:    {},
		StatusAchieved:  {},
		StatusAbandoned: {},
	},
	StatusPaused: {
		StatusActive:    {}, // resumed
		StatusAchieved:  {},
		StatusAbandoned: {},
	},
}

// CanTransition reports whether the status change is allowed.
func CanTransition(from, to Status) bool {
	_, ok := transitions[from][to]
	return ok
}

// MilestoneStatus is the state of one step of the plan.
type MilestoneStatus string

const (
	MilestonePending    MilestoneStatus = "pending"
	MilestoneInProgress MilestoneStatus = "in_progress"
	MilestoneDone       MilestoneStatus = "done"
	MilestoneSkipped    MilestoneStatus = "skipped"
)

// IsValid reports whether the milestone status is known.
func (s MilestoneStatus) IsValid() bool {
	switch s {
	case MilestonePending, MilestoneInProgress, MilestoneDone, MilestoneSkipped:
		return true
	default:
		return false
	}
}

// Done reports whether the milestone was actually completed. A skipped step
// is deliberately not "done": it does not count as progress in percentages.
func (s MilestoneStatus) Done() bool {
	return s == MilestoneDone
}

// Completed reports whether the milestone no longer needs work: it was either
// really done or deliberately skipped. This is what the plan's completeness
// checks use, while progress percentages count only actual completions.
func (s MilestoneStatus) Completed() bool {
	return s == MilestoneDone || s == MilestoneSkipped
}

// TargetType identifies a LifeOS object linked to a goal.
type TargetType string

const (
	TargetConversation TargetType = "conversation"
	TargetTask         TargetType = "task"
	TargetEvent        TargetType = "event"
	TargetDocument     TargetType = "document"
	TargetMemory       TargetType = "memory"
	TargetInbox        TargetType = "inbox"
)

func (t TargetType) IsValid() bool {
	switch t {
	case TargetConversation, TargetTask, TargetEvent, TargetDocument, TargetMemory, TargetInbox:
		return true
	default:
		return false
	}
}

// Link connects a goal to another LifeOS object.
type Link struct {
	ID         int64
	GoalID     int64
	TargetType TargetType
	TargetID   int64
	Relation   string
	CreatedAt  time.Time
}

// ProgressKind classifies one piece of progress evidence.
type ProgressKind string

const (
	ProgressGoalCreated ProgressKind = "goal_created"
	ProgressApproved    ProgressKind = "approved"
	ProgressMilestone   ProgressKind = "milestone"
	ProgressTaskDone    ProgressKind = "task_done"
	ProgressNote        ProgressKind = "note"
	ProgressStatus      ProgressKind = "status"
	ProgressProposal    ProgressKind = "proposal"
)

func (k ProgressKind) IsValid() bool {
	switch k {
	case ProgressGoalCreated, ProgressApproved, ProgressMilestone, ProgressTaskDone,
		ProgressNote, ProgressStatus, ProgressProposal:
		return true
	default:
		return false
	}
}

// ProgressEvent is one recorded piece of factual progress. The journal is the
// "фактические данные" behind progress and stall detection; nothing here is a
// self-assessment of the model.
type ProgressEvent struct {
	ID         int64
	GoalID     int64
	Kind       ProgressKind
	Summary    string
	SourceType *TargetType
	SourceID   *int64
	RecordedAt time.Time
}

// ProposalStatus is the lifecycle of a proposed plan change.
type ProposalStatus string

const (
	ProposalPending  ProposalStatus = "pending"
	ProposalApplied  ProposalStatus = "applied"
	ProposalRejected ProposalStatus = "rejected"
)

func (s ProposalStatus) IsValid() bool {
	switch s {
	case ProposalPending, ProposalApplied, ProposalRejected:
		return true
	default:
		return false
	}
}

// MilestoneInput is one step of a plan as it arrives from a user or the model.
type MilestoneInput struct {
	Title    string
	Criteria string
	DueDate  *time.Time
}

// ProposalPayload is a structured plan revision. Every change is explicit so
// the user can judge exactly what the assistant wants to alter before the
// proposal is applied.
type ProposalPayload struct {
	Title                *string          `json:"title,omitempty"`
	Description          *string          `json:"description,omitempty"`
	TargetDate           *time.Time       `json:"target_date,omitempty"`
	ClearTargetDate      bool             `json:"clear_target_date,omitempty"`
	AddMilestones        []MilestoneInput `json:"add_milestones,omitempty"`
	CompleteMilestoneIDs []int64          `json:"complete_milestone_ids,omitempty"`
	SkipMilestoneIDs     []int64          `json:"skip_milestone_ids,omitempty"`
	ReopenMilestoneIDs   []int64          `json:"reopen_milestone_ids,omitempty"`
	RemoveMilestoneIDs   []int64          `json:"remove_milestone_ids,omitempty"`
}

// Empty reports whether the payload would change nothing.
func (p ProposalPayload) Empty() bool {
	return p.Title == nil && p.Description == nil && p.TargetDate == nil &&
		!p.ClearTargetDate && len(p.AddMilestones) == 0 &&
		len(p.CompleteMilestoneIDs) == 0 && len(p.SkipMilestoneIDs) == 0 &&
		len(p.ReopenMilestoneIDs) == 0 && len(p.RemoveMilestoneIDs) == 0
}

// Proposal is one suggested plan change waiting for the user's decision.
type Proposal struct {
	ID        int64
	GoalID    int64
	Summary   string
	Payload   ProposalPayload
	Status    ProposalStatus
	CreatedAt time.Time
	DecidedAt *time.Time
}

// Milestone is one step of the plan.
type Milestone struct {
	ID          int64
	GoalID      int64
	Position    int
	Title       string
	Criteria    string
	Status      MilestoneStatus
	DueDate     *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int
}

// Goal is one long-term intention with its lifecycle metadata.
type Goal struct {
	ID          int64
	Title       string
	Description string
	Status      Status
	TargetDate  *time.Time
	ApprovedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int
}

// Validation errors.
var (
	ErrInvalidTitle       = errors.New("goal title is required and must be at most 200 characters")
	ErrInvalidDescription = errors.New("goal description must be at most 5000 characters")
	ErrInvalidCriteria    = errors.New("milestone criteria must be at most 2000 characters")
	ErrInvalidMilestone   = errors.New("milestone title is required and must be at most 200 characters")
	ErrInvalidStatus      = errors.New("invalid goal status")
	ErrInvalidMileStatus  = errors.New("invalid milestone status")
	ErrInvalidTarget      = errors.New("invalid goal link target")
	ErrInvalidLink        = errors.New("invalid goal link")
	ErrInvalidRelation    = errors.New("goal relation is required and must be at most 50 characters")
	ErrInvalidSummary     = errors.New("proposal summary is required and must be at most 2000 characters")
	ErrInvalidProgress    = errors.New("invalid progress summary")
	ErrInvalidPayload     = errors.New("proposal payload would change nothing or is invalid")
)

func validTitle(value string) bool {
	value = strings.TrimSpace(value)
	return utf8.ValidString(value) && utf8.RuneCountInString(value) > 0 && utf8.RuneCountInString(value) <= 200
}

// Validate checks user-controlled content of the goal.
func (g *Goal) Validate() error {
	if !validTitle(g.Title) {
		return ErrInvalidTitle
	}
	if !utf8.ValidString(g.Description) || utf8.RuneCountInString(g.Description) > 5000 {
		return ErrInvalidDescription
	}
	if !g.Status.IsValid() {
		return ErrInvalidStatus
	}
	return nil
}

// Validate checks user-controlled content of the milestone.
func (m *Milestone) Validate() error {
	if !validTitle(m.Title) {
		return ErrInvalidMilestone
	}
	if !utf8.ValidString(m.Criteria) || utf8.RuneCountInString(m.Criteria) > 2000 {
		return ErrInvalidCriteria
	}
	if !m.Status.IsValid() {
		return ErrInvalidMileStatus
	}
	return nil
}

// Validate checks user-controlled content of the link.
func (l *Link) Validate() error {
	if l.GoalID <= 0 || l.TargetID <= 0 || !l.TargetType.IsValid() {
		return ErrInvalidLink
	}
	relation := strings.TrimSpace(l.Relation)
	if relation == "" || utf8.RuneCountInString(relation) > 50 {
		return ErrInvalidRelation
	}
	return nil
}

// ValidateMilestoneInput checks one plan step before it is stored.
func ValidateMilestoneInput(input MilestoneInput) error {
	if !validTitle(input.Title) {
		return ErrInvalidMilestone
	}
	if !utf8.ValidString(input.Criteria) || utf8.RuneCountInString(input.Criteria) > 2000 {
		return ErrInvalidCriteria
	}
	return nil
}

// ValidatePayload checks a proposed plan revision.
func (p *ProposalPayload) Validate() error {
	if p.Empty() {
		return ErrInvalidPayload
	}
	if p.Title != nil && !validTitle(*p.Title) {
		return ErrInvalidTitle
	}
	if p.Description != nil &&
		(!utf8.ValidString(*p.Description) || utf8.RuneCountInString(*p.Description) > 5000) {
		return ErrInvalidDescription
	}
	for _, input := range p.AddMilestones {
		if err := ValidateMilestoneInput(input); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the journal entry summary.
func (e *ProgressEvent) Validate() error {
	summary := strings.TrimSpace(e.Summary)
	if !utf8.ValidString(summary) || utf8.RuneCountInString(summary) == 0 || utf8.RuneCountInString(summary) > 1000 {
		return ErrInvalidProgress
	}
	if !e.Kind.IsValid() {
		return ErrInvalidStatus
	}
	// A source is an optional pair: type and a positive id travel together, so
	// a journal entry can never point at id 0 or a negative object.
	if e.SourceType != nil || e.SourceID != nil {
		if e.SourceType == nil || e.SourceID == nil || !e.SourceType.IsValid() || *e.SourceID <= 0 {
			return ErrInvalidTarget
		}
	}
	return nil
}

// Validate checks the proposal summary before it is stored.
func (p *Proposal) Validate() error {
	summary := strings.TrimSpace(p.Summary)
	if !utf8.ValidString(summary) || utf8.RuneCountInString(summary) == 0 || utf8.RuneCountInString(summary) > 2000 {
		return ErrInvalidSummary
	}
	if err := p.Payload.Validate(); err != nil {
		return err
	}
	return nil
}
