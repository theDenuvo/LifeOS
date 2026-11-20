// Package skill owns the model of assistant skills: a reusable, versioned
// instruction for a class of tasks — when it applies, which steps to follow,
// which limits to respect and how to check the result.
//
// The product rule behind the model: a skill describes a proven way to use
// knowledge and tools; it never replaces memory (facts about the user) or
// tools (technical actions), and it never grants new permissions, bypasses
// safety rules or adds tools and data access. Creating, changing, disabling
// or deleting an active skill is a user decision: the assistant drafts, the
// user activates, and every applied change becomes a new version that can be
// compared and rolled back.
package skill

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits keep one skill readable as a single instruction block inside a
// prompt. They are product limits, not storage limits: a longer text would
// crowd out the conversation it is meant to help.
const (
	MaxNameRunes         = 80
	MaxPurposeRunes      = 1000
	MaxWhenRunes         = 1000
	MaxInstructionsRunes = 20000
	MaxLimitationsRunes  = 4000
	MaxExamplesRunes     = 8000
	MaxTags              = 10
	MaxTagRunes          = 30
	MaxProblemRunes      = 2000
	MaxEffectRunes       = 2000
	MaxTestRunes         = 4000
	MaxCommentRunes      = 2000
)

// Status is the lifecycle of a skill.
//
// draft — created by the user or the assistant, not injected into turns;
// active — injected when its conditions match; disabled — kept, not injected;
// archived — retired, kept for history, not injected.
type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusArchived Status = "archived"
)

// IsValid reports whether the status is known.
func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusDisabled, StatusArchived:
		return true
	default:
		return false
	}
}

// Injected reports whether the skill may be connected to a turn.
func (s Status) Injected() bool { return s == StatusActive }

// transitions lists the lifecycle moves the product rules allow. Archived
// may return to draft (restore for editing); it never jumps straight to
// active, because re-activation is a fresh decision on a reviewed text.
var transitions = map[Status]map[Status]struct{}{
	StatusDraft: {
		StatusActive:   {},
		StatusArchived: {},
	},
	StatusActive: {
		StatusDisabled: {},
		StatusArchived: {},
	},
	StatusDisabled: {
		StatusActive:   {},
		StatusArchived: {},
	},
	StatusArchived: {
		StatusDraft: {},
	},
}

// CanTransition reports whether the status change is allowed.
func CanTransition(from, to Status) bool {
	_, ok := transitions[from][to]
	return ok
}

// Source records who authored a skill. Assistant-authored skills always
// start as drafts; user-authored ones are the user's own decision.
type Source string

const (
	SourceUser      Source = "user"
	SourceAssistant Source = "assistant"
	SourceSystem    Source = "system"
)

// IsValid reports whether the source is known.
func (s Source) IsValid() bool {
	switch s {
	case SourceUser, SourceAssistant, SourceSystem:
		return true
	default:
		return false
	}
}

// ProposalKind selects what an improvement proposal changes.
type ProposalKind string

const (
	ProposalSkillVersion        ProposalKind = "skill_version"
	ProposalSkillCreate         ProposalKind = "skill_create"
	ProposalWorkingInstructions ProposalKind = "working_instructions"
)

// IsValid reports whether the proposal kind is known.
func (k ProposalKind) IsValid() bool {
	switch k {
	case ProposalSkillVersion, ProposalSkillCreate, ProposalWorkingInstructions:
		return true
	default:
		return false
	}
}

// ProposalStatus is the decision state of an improvement proposal.
type ProposalStatus string

const (
	ProposalProposed ProposalStatus = "proposed"
	ProposalApplied  ProposalStatus = "applied"
	ProposalRejected ProposalStatus = "rejected"
)

// IsValid reports whether the proposal status is known.
func (s ProposalStatus) IsValid() bool {
	switch s {
	case ProposalProposed, ProposalApplied, ProposalRejected:
		return true
	default:
		return false
	}
}

// Rating is a user's judgement of how a skill worked. It feeds later
// analysis; by itself it never rewrites an active instruction.
type Rating string

const (
	RatingGood Rating = "good"
	RatingBad  Rating = "bad"
)

// IsValid reports whether the rating is known.
func (r Rating) IsValid() bool { return r == RatingGood || r == RatingBad }

// Skill is one versioned instruction in the library. The current fields
// mirror the current version; history lives in the version rows.
type Skill struct {
	ID                 int64
	Name               string
	Purpose            string
	WhenToUse          string
	Instructions       string
	Limitations        string
	Examples           string
	Tags               []string
	Status             Status
	CurrentVersion     int
	Source             Source
	CreatedByAssistant bool
	UseCount           int64
	GoodCount          int64
	BadCount           int64
	LastUsedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Version            int
}

// Content is the editable instruction body shared by a skill, a version
// and a proposal, so a change can be compared field by field.
type Content struct {
	Name         string
	Purpose      string
	WhenToUse    string
	Instructions string
	Limitations  string
	Examples     string
	Tags         []string
}

// Version is one immutable entry in a skill's history.
type Version struct {
	ID         int64
	SkillID    int64
	VersionNo  int
	Content    Content
	ChangeNote string
	CreatedBy  Source
	CreatedAt  time.Time
}

// Proposal is one user-gated improvement: the found problem, the evidence
// behind it, the exact proposed change, its expected effect and the test
// result obtained before the change is applied.
type Proposal struct {
	ID             int64
	SkillID        *int64
	Kind           ProposalKind
	Status         ProposalStatus
	Problem        string
	Evidence       []string
	Proposed       Content
	ExpectedEffect string
	TestScenarios  []string
	TestResult     string
	BaseVersion    *int
	// BaseWorkingHash is the fingerprint of the working instructions
	// text the proposal was prepared against (working-instructions
	// proposals only). A nil value means the proposal predates the
	// optimistic check and applies unconditionally.
	BaseWorkingHash *string
	ConversationID  *int64
	ResultSkillID   *int64
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	Version         int
}

// Feedback is one user judgement of a skill.
type Feedback struct {
	ID             int64
	SkillID        int64
	VersionNo      int
	Rating         Rating
	Comment        string
	ConversationID *int64
	CreatedAt      time.Time
}

// Usage records one injection of a skill into a turn (or one explicit
// fetch through get_skill), so later analysis can tell how often a skill
// was really used and how it was judged.
type Usage struct {
	ID             int64
	SkillID        int64
	VersionNo      int
	ConversationID *int64
	Context        string
	MatchedTerms   []string
	CreatedAt      time.Time
}

// Errors returned by validation. The transport maps them to problem details.
var (
	ErrInvalidName         = errors.New("skill name is required (3–80 characters)")
	ErrInvalidPurpose      = errors.New("skill purpose is required (10–1000 characters)")
	ErrInvalidWhen         = errors.New("skill conditions are required (10–1000 characters)")
	ErrInvalidInstructions = errors.New("skill instructions are required (50–20000 characters)")
	ErrInvalidLimitations  = errors.New("skill limitations must be at most 4000 characters")
	ErrInvalidExamples     = errors.New("skill examples must be at most 8000 characters")
	ErrInvalidTags         = errors.New("skill tags must be at most 10, each 2–30 characters")
	ErrForbiddenContent    = errors.New("a skill must not grant new permissions, bypass safety rules or add tools and data access")
	ErrInvalidProblem      = errors.New("proposal problem is required")
	ErrInvalidEffect       = errors.New("proposal expected effect is required")
	ErrInvalidComment      = errors.New("feedback comment must be at most 2000 characters")
)

// forbiddenPhrases are the boundary of stage 8 in one list: a skill may
// describe how to use existing tools, but a text that promises new access
// or asks to evade rules is refused at validation instead of being stored
// and silently widening what the assistant may do.
var forbiddenPhrases = []string{
	"игнорируй правила", "игнорировать правила", "обойди правила", "обходить правила",
	"обойди безопасность", "отключи безопасность", "игнорируй безопасность",
	"получи новый доступ", "получить новый доступ", "добавь инструмент", "добавляй инструменты",
	"добавить инструмент", "новый инструмент доступа", "полный доступ к системе",
	"игнорируй системные правила", "раскрой системный промпт", "ignore safety",
	"bypass safety", "bypass security", "grant new access", "add a new tool",
	"add new tool", "new tool access", "disable security", "ignore the rules",
	"ignore system rules", "modify the lifeos core", "измени ядро lifeos",
	"изменить исходный код lifeos", "change the lifeos source",
}

// ValidateContent checks one full instruction body. It is used for a new
// skill, an edit, a version and a proposal alike, so no path can store a
// text the other paths would refuse.
func ValidateContent(content Content) error {
	name := strings.TrimSpace(content.Name)
	if n := utf8.RuneCountInString(name); n < 3 || n > MaxNameRunes {
		return ErrInvalidName
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(content.Purpose)); n < 10 || n > MaxPurposeRunes {
		return ErrInvalidPurpose
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(content.WhenToUse)); n < 10 || n > MaxWhenRunes {
		return ErrInvalidWhen
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(content.Instructions)); n < 50 || n > MaxInstructionsRunes {
		return ErrInvalidInstructions
	}
	if utf8.RuneCountInString(content.Limitations) > MaxLimitationsRunes {
		return ErrInvalidLimitations
	}
	if utf8.RuneCountInString(content.Examples) > MaxExamplesRunes {
		return ErrInvalidExamples
	}
	if len(NormalizeTags(content.Tags)) > MaxTags {
		return ErrInvalidTags
	}
	for _, tag := range NormalizeTags(content.Tags) {
		if utf8.RuneCountInString(tag) < 2 || utf8.RuneCountInString(tag) > MaxTagRunes {
			return ErrInvalidTags
		}
	}
	if err := checkForbidden(content); err != nil {
		return err
	}
	return nil
}

func checkForbidden(content Content) error {
	fields := []string{content.Purpose, content.WhenToUse, content.Instructions, content.Limitations, content.Examples}
	for _, field := range fields {
		lowered := strings.ToLower(field)
		for _, phrase := range forbiddenPhrases {
			if strings.Contains(lowered, phrase) {
				return fmt.Errorf("%w: найдена формулировка %q", ErrForbiddenContent, phrase)
			}
		}
	}
	return nil
}

// ValidateProposal checks the decision payload of an improvement proposal.
func ValidateProposal(problem string, effect string, proposed Content, kind ProposalKind) error {
	if strings.TrimSpace(problem) == "" || utf8.RuneCountInString(problem) > MaxProblemRunes {
		return ErrInvalidProblem
	}
	if strings.TrimSpace(effect) == "" || utf8.RuneCountInString(effect) > MaxEffectRunes {
		return ErrInvalidEffect
	}
	// A working-instructions proposal carries only the new instruction text;
	// skill proposals carry a full skill body.
	if kind == ProposalWorkingInstructions {
		if n := utf8.RuneCountInString(strings.TrimSpace(proposed.Instructions)); n < 20 || n > MaxInstructionsRunes {
			return ErrInvalidInstructions
		}
		return checkForbidden(proposed)
	}
	return ValidateContent(proposed)
}

// NormalizeName returns the comparison form used for uniqueness: trimmed,
// single-spaced and lower-cased. Display keeps the author's case.
func NormalizeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

// NormalizeTags lower-cases, trims and de-duplicates tags, preserving order.
func NormalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		value := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(tag))), " ")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// (s *Skill) Content returns the current instruction body.
func (s *Skill) Content() Content {
	return Content{
		Name: s.Name, Purpose: s.Purpose, WhenToUse: s.WhenToUse,
		Instructions: s.Instructions, Limitations: s.Limitations,
		Examples: s.Examples, Tags: append([]string(nil), s.Tags...),
	}
}

// ApplyContent replaces the current body; the caller records a version.
func (s *Skill) ApplyContent(content Content) {
	s.Name = strings.TrimSpace(content.Name)
	s.Purpose = strings.TrimSpace(content.Purpose)
	s.WhenToUse = strings.TrimSpace(content.WhenToUse)
	s.Instructions = strings.TrimSpace(content.Instructions)
	s.Limitations = strings.TrimSpace(content.Limitations)
	s.Examples = strings.TrimSpace(content.Examples)
	s.Tags = NormalizeTags(content.Tags)
}

// BadRatio is the share of bad judgements among all judgements (0–1).
func (s *Skill) BadRatio() float64 {
	total := s.GoodCount + s.BadCount
	if total == 0 {
		return 0
	}
	return float64(s.BadCount) / float64(total)
}

// Diff returns the human explanation of what changed between two bodies.
// It is the same comparison the interface shows before a proposal is
// applied and the same one a rollback restores.
func Diff(before, after Content) []string {
	var out []string
	if NormalizeName(before.Name) != NormalizeName(after.Name) {
		out = append(out, fmt.Sprintf("название: «%s» → «%s»", before.Name, after.Name))
	}
	if strings.TrimSpace(before.Purpose) != strings.TrimSpace(after.Purpose) {
		out = append(out, "назначение изменено")
	}
	if strings.TrimSpace(before.WhenToUse) != strings.TrimSpace(after.WhenToUse) {
		out = append(out, "условия применения изменены")
	}
	if strings.TrimSpace(before.Instructions) != strings.TrimSpace(after.Instructions) {
		out = append(out, "инструкции изменены")
	}
	if strings.TrimSpace(before.Limitations) != strings.TrimSpace(after.Limitations) {
		out = append(out, "ограничения изменены")
	}
	if strings.TrimSpace(before.Examples) != strings.TrimSpace(after.Examples) {
		out = append(out, "примеры изменены")
	}
	if strings.Join(NormalizeTags(before.Tags), ",") != strings.Join(NormalizeTags(after.Tags), ",") {
		out = append(out, "теги изменены")
	}
	return out
}
