// Package memory owns durable personal facts, reusable knowledge, their
// provenance, and the relationships between them and the rest of LifeOS.
package memory

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Area separates a model of the user from reusable reference knowledge.
type Area string

const (
	AreaPersonal  Area = "personal"
	AreaKnowledge Area = "knowledge"
)

// Category describes why an entry is useful. Categories are deliberately
// semantic rather than tied to a fixed set of life domains.
type Category string

const (
	CategoryPreference    Category = "preference"
	CategoryHabit         Category = "habit"
	CategoryCircumstance  Category = "circumstance"
	CategoryFact          Category = "fact"
	CategoryAgreement     Category = "agreement"
	CategoryContext       Category = "context"
	CategoryRule          Category = "rule"
	CategoryInstruction   Category = "instruction"
	CategorySummary       Category = "summary"
	CategoryReference     Category = "reference"
	CategoryResearch      Category = "research"
	CategoryIdea          Category = "idea"
	CategoryDocumentation Category = "documentation"
	CategoryNote          Category = "note"
)

var personalCategories = map[Category]struct{}{
	CategoryPreference: {}, CategoryHabit: {}, CategoryCircumstance: {},
	CategoryFact: {}, CategoryAgreement: {}, CategoryContext: {}, CategoryRule: {},
}

var knowledgeCategories = map[Category]struct{}{
	CategoryInstruction: {}, CategorySummary: {}, CategoryReference: {},
	CategoryResearch: {}, CategoryIdea: {}, CategoryDocumentation: {}, CategoryNote: {},
}

// IsValid reports whether the category is known.
func (c Category) IsValid() bool {
	_, personal := personalCategories[c]
	_, knowledge := knowledgeCategories[c]
	return personal || knowledge
}

// Area returns the part of memory to which the category belongs.
func (c Category) Area() Area {
	if _, ok := personalCategories[c]; ok {
		return AreaPersonal
	}
	return AreaKnowledge
}

// Certainty prevents source facts, assumptions and system inferences from
// silently becoming interchangeable.
type Certainty string

const (
	CertaintyFact       Certainty = "fact"
	CertaintyAssumption Certainty = "assumption"
	CertaintyInference  Certainty = "inference"
)

func (c Certainty) IsValid() bool {
	return c == CertaintyFact || c == CertaintyAssumption || c == CertaintyInference
}

// Status is the lifecycle of a memory entry. Superseded entries remain as an
// auditable version chain but never enter the assistant's active snapshot.
type Status string

const (
	StatusActive     Status = "active"
	StatusDisputed   Status = "disputed"
	StatusSuperseded Status = "superseded"
)

func (s Status) IsValid() bool {
	return s == StatusActive || s == StatusDisputed || s == StatusSuperseded
}

// SourceKind identifies a provenance record without coupling memory to the
// database tables of other modules.
type SourceKind string

const (
	SourceUser         SourceKind = "user"
	SourceAssistant    SourceKind = "assistant"
	SourceConversation SourceKind = "conversation"
	SourceDocument     SourceKind = "document"
	SourceTask         SourceKind = "task"
	SourceEvent        SourceKind = "event"
	SourceSystem       SourceKind = "system"
)

func (k SourceKind) IsValid() bool {
	switch k {
	case SourceUser, SourceAssistant, SourceConversation, SourceDocument, SourceTask, SourceEvent, SourceSystem:
		return true
	default:
		return false
	}
}

// Source is one independently inspectable origin of an entry. Exact duplicate
// facts accumulate sources instead of creating duplicate entries.
type Source struct {
	ID         int64
	Kind       SourceKind
	SourceID   int64
	Label      string
	Excerpt    string
	ObservedAt time.Time
}

// Entry is one durable statement or reusable knowledge document.
type Entry struct {
	ID              int64
	Title           string
	NormalizedTitle string
	Content         string
	DedupeKey       string
	DocumentSHA     string
	Category        Category
	Certainty       Certainty
	Confidence      float64
	Status          Status
	SupersededBy    *int64
	Entities        []string
	Sources         []Source
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Version         int
}

// Area derives the entry area from its category.
func (e *Entry) Area() Area { return e.Category.Area() }

// Validation errors.
var (
	ErrInvalidTitle      = errors.New("memory title is required and must be at most 200 characters")
	ErrInvalidContent    = errors.New("memory content is required and must be at most 100000 characters")
	ErrInvalidCategory   = errors.New("invalid memory category")
	ErrInvalidCertainty  = errors.New("invalid memory certainty")
	ErrInvalidStatus     = errors.New("invalid memory status")
	ErrInvalidConfidence = errors.New("memory confidence must be between 0 and 1")
	ErrInvalidSource     = errors.New("invalid memory source")
	ErrInvalidEntities   = errors.New("invalid memory entities")
	ErrInvalidEntry      = errors.New("invalid memory entry")
)

// Validate checks user-controlled content and provenance.
func (e *Entry) Validate() error {
	title := strings.TrimSpace(e.Title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) == 0 || utf8.RuneCountInString(title) > 200 {
		return ErrInvalidTitle
	}
	content := strings.TrimSpace(e.Content)
	if !utf8.ValidString(content) || utf8.RuneCountInString(content) == 0 || utf8.RuneCountInString(content) > 100000 {
		return ErrInvalidContent
	}
	if !e.Category.IsValid() {
		return ErrInvalidCategory
	}
	if !e.Certainty.IsValid() {
		return ErrInvalidCertainty
	}
	if !e.Status.IsValid() {
		return ErrInvalidStatus
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return ErrInvalidConfidence
	}
	if len(e.Sources) == 0 {
		return ErrInvalidSource
	}
	for _, source := range e.Sources {
		if !source.Kind.IsValid() || !utf8.ValidString(source.Excerpt) || utf8.RuneCountInString(source.Excerpt) > 2000 || utf8.RuneCountInString(source.Label) > 500 {
			return ErrInvalidSource
		}
	}
	if len(e.Entities) > 30 {
		return ErrInvalidEntities
	}
	for _, entity := range e.Entities {
		if !utf8.ValidString(entity) || utf8.RuneCountInString(strings.TrimSpace(entity)) == 0 || utf8.RuneCountInString(entity) > 100 {
			return ErrInvalidEntities
		}
	}
	return nil
}

// TargetType identifies a LifeOS object linked to memory.
type TargetType string

const (
	TargetMemory       TargetType = "memory"
	TargetTask         TargetType = "task"
	TargetEvent        TargetType = "event"
	TargetConversation TargetType = "conversation"
	TargetDocument     TargetType = "document"
	TargetInbox        TargetType = "inbox"
	TargetGoal         TargetType = "goal"
)

func (t TargetType) IsValid() bool {
	switch t {
	case TargetMemory, TargetTask, TargetEvent, TargetConversation, TargetDocument, TargetInbox, TargetGoal:
		return true
	default:
		return false
	}
}

// Link connects a memory entry to another entry or a LifeOS object.
type Link struct {
	ID         int64
	EntryID    int64
	TargetType TargetType
	TargetID   int64
	Relation   string
	CreatedAt  time.Time
}

var (
	ErrInvalidLink     = errors.New("invalid memory link")
	ErrInvalidRelation = errors.New("memory relation is required and must be at most 50 characters")
)

func (l *Link) Validate() error {
	if l.EntryID <= 0 || l.TargetID <= 0 || !l.TargetType.IsValid() {
		return ErrInvalidLink
	}
	if l.TargetType == TargetMemory && l.TargetID == l.EntryID {
		return ErrInvalidLink
	}
	relation := strings.TrimSpace(l.Relation)
	if relation == "" || utf8.RuneCountInString(relation) > 50 {
		return ErrInvalidRelation
	}
	return nil
}

// ConflictStatus describes whether the user still needs to make a decision.
type ConflictStatus string

const (
	ConflictPending  ConflictStatus = "pending"
	ConflictResolved ConflictStatus = "resolved"
)

// Conflict retains two incompatible claims and the reason they need review.
type Conflict struct {
	ID          int64
	ExistingID  int64
	CandidateID int64
	Reason      string
	Status      ConflictStatus
	Resolution  string
	CreatedAt   time.Time
	ResolvedAt  *time.Time
}
