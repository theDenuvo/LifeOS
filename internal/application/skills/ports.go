// Package skills implements the assistant skill library: versioned
// instructions the assistant drafts and the user activates, relevant-only
// injection into turns, user-gated improvement proposals and feedback that
// feeds later analysis without silently rewriting an active instruction.
package skills

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// Errors shared by the service and the transports.
var (
	ErrNotFound          = errors.New("skill not found")
	ErrProposalNotFound  = errors.New("skill proposal not found")
	ErrVersionNotFound   = errors.New("skill version not found")
	ErrVersionConflict   = errors.New("skill was changed by another operation")
	ErrInvalidTransition = errors.New("skill status transition is not allowed")
	ErrDeleteActive      = errors.New("only a draft or archived skill can be deleted; disable or archive it first")
	ErrEmptyUpdate       = errors.New("the update would change nothing")
	ErrProposalResolved  = errors.New("the proposal is not pending anymore")
	ErrStaleBase         = errors.New("the skill changed since the proposal was prepared; the proposal is stale")
	ErrNoWorkingStore    = errors.New("working instructions storage is not configured")
)

// Clock supplies timestamps to all mutations.
type Clock interface{ Now() time.Time }

// Filter bounds skill listing.
type Filter struct {
	Query  string
	Status *domainskill.Status
	Limit  int
	Offset int
}

// CreateInput is one drafted skill. It always starts as a draft: activation
// is a separate user decision, whoever created the text.
type CreateInput struct {
	Content    domainskill.Content
	ChangeNote string
}

// UpdateInput is a partial, optimistic-locked edit. A nil field keeps its
// current value. A real change creates a new version; history is never
// rewritten.
type UpdateInput struct {
	Name         *string
	Purpose      *string
	WhenToUse    *string
	Instructions *string
	Limitations  *string
	Examples     *string
	Tags         *[]string
	ChangeNote   string
}

// ProposalInput is one prepared improvement. The proposal changes nothing
// until the user applies it.
type ProposalInput struct {
	SkillID        *int64
	Kind           domainskill.ProposalKind
	Problem        string
	Evidence       []string
	Proposed       domainskill.Content
	ExpectedEffect string
	TestScenarios  []string
	// BaseWorkingHash is the fingerprint of the working instructions
	// text the proposal was built from (working-instructions proposals
	// only). The analysis passes it through so the stored hash always
	// describes the text the proposal text derives from, never a later
	// manual edit. Nil means CreateProposal reads the current text.
	BaseWorkingHash *string
	// BaseVersion is the content version the proposal text was built
	// from (skill proposals). The analysis and the tools hand it
	// through so the stored base always describes the snapshot the
	// proposal derives from, never a later manual edit. Nil means
	// CreateProposal reads the current version.
	BaseVersion    *int
	ConversationID *int64
}

// Repository is the persistence boundary of the skill aggregate, its
// versions, proposals, feedback and usage journal.
type Repository interface {
	InsertSkill(ctx context.Context, skill *domainskill.Skill, first domainskill.Version) (int64, error)
	GetSkill(ctx context.Context, id int64) (*domainskill.Skill, error)
	GetSkillByName(ctx context.Context, normalizedName string) (*domainskill.Skill, error)
	ListSkills(ctx context.Context, filter Filter) ([]*domainskill.Skill, int, error)
	// UpdateSkill performs a conditional write and, when newVersion is not
	// nil, appends it as the current version in the same transaction.
	UpdateSkill(ctx context.Context, id int64, expectedVersion int, skill *domainskill.Skill, newVersion *domainskill.Version) (*domainskill.Skill, error)
	DeleteSkill(ctx context.Context, id int64) error
	ListVersions(ctx context.Context, skillID int64) ([]domainskill.Version, error)
	GetVersion(ctx context.Context, skillID int64, versionNo int) (*domainskill.Version, error)

	InsertProposal(ctx context.Context, proposal *domainskill.Proposal) (int64, error)
	GetProposal(ctx context.Context, id int64) (*domainskill.Proposal, error)
	ListProposals(ctx context.Context, skillID int64, status *domainskill.ProposalStatus) ([]*domainskill.Proposal, error)
	UpdateProposal(ctx context.Context, id int64, expectedVersion int, proposal *domainskill.Proposal) (*domainskill.Proposal, error)

	// RecordFeedback stores one judgement and moves the skill's counters in
	// the same transaction, so the counters always match the journal.
	RecordFeedback(ctx context.Context, feedback *domainskill.Feedback) (int64, error)
	ListFeedback(ctx context.Context, skillID int64, limit int) ([]domainskill.Feedback, error)
	// RecordUsage stores one injection and moves use_count/last_used_at in
	// the same transaction.
	RecordUsage(ctx context.Context, usage *domainskill.Usage) (int64, error)
	ListUsages(ctx context.Context, skillID int64, limit int) ([]domainskill.Usage, error)
}

// WorkingInstructionsKey is the settings key of the assistant's working
// instructions block. ApplyProposal reads and writes it inside the same
// transaction as the proposal, so a stale base is refused instead of
// silently overwritten.
const WorkingInstructionsKey = "assistant.working_instructions"

// SettingsRepository is the subset of the settings store a skill
// transaction may touch. It is the same table settings.Service uses, but
// on the transaction `tx` already owns: with SetMaxOpenConns(1) a nested
// settings.Service.Put() would wait forever for the connection ApplyProposal
// is holding. Working-instruction proposals must therefore go through this
// interface, not through WorkingInstructionsStore, inside a transaction.
type SettingsRepository interface {
	Get(ctx context.Context, key string) (json.RawMessage, bool, error)
	Put(ctx context.Context, key string, value json.RawMessage, at time.Time) error
}

// Transaction exposes one transactional skill repository and the settings
// store on the same connection.
type Transaction interface {
	Skills() Repository
	Settings() SettingsRepository
}

// UnitOfWork is the skill transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(context.Context, Transaction) error) error
}

// WorkingInstructionsStore persists the assistant's working instructions
// block. It is the settings store in production; without it working-
// instruction proposals cannot be applied and the service says so instead
// of pretending they were saved.
type WorkingInstructionsStore interface {
	GetWorkingInstructions(ctx context.Context) (string, error)
	SetWorkingInstructions(ctx context.Context, text string) error
}

// View is one skill with its history and decision state.
type View struct {
	Skill     *domainskill.Skill
	Versions  []domainskill.Version
	Proposals []*domainskill.Proposal
	Feedback  []domainskill.Feedback
	Usages    []domainskill.Usage
}

// Finding is one evidence-backed improvement the analysis found. It is a
// proposal before it becomes one: nothing is stored until the caller
// creates proposals from it.
type Finding struct {
	SkillID        *int64
	SkillName      string
	Kind           domainskill.ProposalKind
	Problem        string
	Evidence       []string
	Proposed       domainskill.Content
	ExpectedEffect string
	TestScenarios  []string
	TestResult     string
	// BaseWorkingHash is the fingerprint of the working instructions
	// text this finding was derived from (working-instructions findings
	// only). It travels with the finding so CreateProposal stores the
	// hash of the very text the proposal builds on.
	BaseWorkingHash *string
	// BaseVersion is the content version of the snapshot this finding
	// was derived from (skill findings). It travels with the finding so
	// CreateProposal stores the version the proposal builds on.
	BaseVersion *int
}
