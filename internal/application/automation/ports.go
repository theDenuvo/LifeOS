// Package automation turns recurring processes into durable, user-approved
// automations: the assistant designs a scenario of LifeOS tool steps, the
// user approves it, and the runner executes it by time, by LifeOS event or
// while a proactive condition holds. Every run is journalled step by step, so
// failures explain themselves instead of disappearing.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

// Errors shared by the service and the transports.
var (
	ErrNotFound          = errors.New("automation not found")
	ErrRunNotFound       = errors.New("automation run not found")
	ErrVersionConflict   = errors.New("automation was changed by another operation")
	ErrInvalidTransition = errors.New("automation status transition is not allowed")
	ErrNotExecutable     = errors.New("only an active automation can run")
	ErrStepNotAllowed    = errors.New("the tool is not allowed inside an automation")
	ErrTriggerMismatch   = errors.New("the trigger does not match the automation")
	ErrAlreadyRunning    = errors.New("a run of this automation is already in progress")
)

// Clock supplies timestamps to all mutations.
type Clock interface{ Now() time.Time }

// Filter bounds automation listing.
type Filter struct {
	Query  string
	Status *domainautomation.Status
	Limit  int
	Offset int
}

// CreateInput is one drafted automation with its scenario.
type CreateInput struct {
	Title          string
	Description    string
	Trigger        domainautomation.Trigger
	Steps          []domainautomation.Step
	ExpectedResult string
	Consequences   string
}

// UpdateInput is a full configuration edit guarded by the optimistic version.
type UpdateInput struct {
	Title          *string
	Description    *string
	Trigger        *domainautomation.Trigger
	Steps          []domainautomation.Step
	ExpectedResult *string
	Consequences   *string
}

// StepValidator is the slice of the tool registry that decides which tools
// may appear inside a scenario. It exists so the service can refuse steps it
// would not be able (or not be allowed) to run.
type StepValidator interface {
	// AllowedForAutomation reports whether the tool may run unattended and,
	// when it may not, why. The reason is shown to the designer.
	AllowedForAutomation(name string) (bool, string)
}

// ToolExecutor runs one step of a scenario.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, arguments json.RawMessage) (any, error)
}

// ConditionWatcher is the slice of the proactive service a condition trigger
// needs: which insights are present right now.
type ConditionWatcher interface {
	CurrentInsightKeys(ctx context.Context) (map[string]struct{}, error)
}

// Repository is the persistence boundary of the automation aggregate and its
// run journal.
type Repository interface {
	Insert(ctx context.Context, automation *domainautomation.Automation) (int64, error)
	Get(ctx context.Context, id int64) (*domainautomation.Automation, error)
	// List returns the page and the total count. Rows are ordered newest
	// first.
	List(ctx context.Context, filter Filter) ([]*domainautomation.Automation, int, error)
	// Update performs a conditional write: the write happens only when the
	// version still matches, and the row's version is incremented.
	Update(ctx context.Context, id int64, expectedVersion int, automation *domainautomation.Automation) (*domainautomation.Automation, error)
	Delete(ctx context.Context, id int64) error

	// ScheduleDue returns active automations whose schedule moment has come:
	// either the stored NextRunAt is in the past, or the bookmark is missing
	// and needs to be (re)computed.
	ScheduleDue(ctx context.Context, now time.Time) ([]*domainautomation.Automation, error)
	// ListByTrigger returns active automations of one trigger kind.
	ListByTrigger(ctx context.Context, kind domainautomation.TriggerKind) ([]*domainautomation.Automation, error)

	// StartRun reserves the automation for one execution inside the same
	// transaction: it creates a running run only when no run of this
	// automation is running. A nil result means another run holds it.
	StartRun(ctx context.Context, automationID int64, trigger string, startedAt time.Time) (*domainautomation.Run, error)
	// FinishRun stores the step journal and the final status.
	FinishRun(ctx context.Context, run *domainautomation.Run) error
	GetRun(ctx context.Context, automationID, runID int64) (*domainautomation.Run, error)
	ListRuns(ctx context.Context, automationID int64, limit, offset int) ([]*domainautomation.Run, int, error)
	// FailRun marks a reserved run failed without a step journal. It is the
	// best-effort cleanup when the normal finish failed: a reserved run must
	// not hold the automation hostage until the next restart.
	FailRun(ctx context.Context, runID int64, reason string, at time.Time) error
	// RecoverRunning releases the reservations a crashed process left
	// behind. LifeOS is single-process, so at startup every running run is
	// stale by definition; they are recorded as failed with the reason.
	RecoverRunning(ctx context.Context, at time.Time) (int64, error)
	// RecordSkipped stores a visible journal entry for a launch that was
	// refused before any step ran: chain-depth limit, an overlapping run or
	// a full dispatch queue. Lost work stays in the journal instead of
	// disappearing silently.
	RecordSkipped(ctx context.Context, automationID int64, trigger, reason string, at time.Time) (int64, error)
}

// Transaction exposes one transactional automation repository.
type Transaction interface{ Automations() Repository }

// UnitOfWork is the automation transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(context.Context, Transaction) error) error
}

// View is one automation with its recent runs.
type View struct {
	Automation *domainautomation.Automation
	Runs       []*domainautomation.Run
}

// RunStats counts the recent executions of one automation.
type RunStats struct {
	Total     int
	Succeeded int
	Failed    int
}

// TickResult summarises one runner pass.
type TickResult struct {
	ScheduleRuns  int
	ConditionRuns int
	// OverdueCatchups counts schedule automations whose stored moment was in
	// the past at pass start: these are missed moments the runner is
	// catching up on.
	OverdueCatchups int
	SkippedBusy     int
}

// ObserveResult reports what one LifeOS event did to the automations.
type ObserveResult struct {
	Matched int
	Runs    int
}

// maxChainDepth bounds chains of automations triggering automations. An event
// published while an automation run executes may be caused by that run; a run
// started from it is one hop deeper. From depth maxChainDepth event-triggered
// runs are refused and recorded as skipped, so no cycle — including the
// automation reacting to its own effects — can sustain itself.
const maxChainDepth = 3
