// Package memory implements durable personal memory and the living knowledge
// base. It deliberately depends only on domain types and narrow ports.
package memory

import (
	"context"
	"errors"
	"time"

	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

var (
	ErrNotFound           = errors.New("memory entry not found")
	ErrLinkNotFound       = errors.New("memory link not found")
	ErrConflictNotFound   = errors.New("memory conflict not found")
	ErrVersionConflict    = errors.New("memory entry was changed by another operation")
	ErrConflictResolved   = errors.New("memory conflict is already resolved")
	ErrInvalidResolution  = errors.New("invalid conflict resolution")
	ErrLinkTargetNotFound = errors.New("memory link target not found")
)

// Clock supplies timestamps to all mutations.
type Clock interface{ Now() time.Time }

// Filter bounds memory listing and search.
type Filter struct {
	Query           string
	Area            *domainmemory.Area
	Category        *domainmemory.Category
	Status          *domainmemory.Status
	IncludeDisputed bool
	Limit           int
	Offset          int
}

// Stats is the compact state used by the workspace and assistant.
type Stats struct {
	Personal         int
	Knowledge        int
	PendingConflicts int
}

// Repository is the persistence boundary for the memory aggregate.
type Repository interface {
	InsertEntry(ctx context.Context, entry *domainmemory.Entry) (int64, error)
	GetEntry(ctx context.Context, id int64) (*domainmemory.Entry, error)
	UpdateEntry(ctx context.Context, id int64, expectedVersion int, entry *domainmemory.Entry) (*domainmemory.Entry, error)
	SetStatus(ctx context.Context, id int64, expectedVersion int, status domainmemory.Status, supersededBy *int64, at time.Time) (*domainmemory.Entry, error)
	DeleteEntry(ctx context.Context, id int64, expectedVersion int) error
	ListEntries(ctx context.Context, filter Filter) ([]*domainmemory.Entry, int, error)
	FindSameSubject(ctx context.Context, area domainmemory.Area, category domainmemory.Category, normalizedTitle string) ([]*domainmemory.Entry, error)
	FindByDedupeKey(ctx context.Context, key string) (*domainmemory.Entry, error)
	AddSource(ctx context.Context, entryID int64, source domainmemory.Source) error
	Stats(ctx context.Context) (Stats, error)

	InsertLink(ctx context.Context, link *domainmemory.Link) (int64, error)
	ListLinks(ctx context.Context, entryID int64) ([]domainmemory.Link, error)
	DeleteLink(ctx context.Context, id, entryID int64) error
	// TargetExists verifies a link target inside the same transaction. Checking
	// through a second connection would deadlock on a single-writer SQLite.
	TargetExists(ctx context.Context, target domainmemory.TargetType, id int64) (bool, error)

	InsertConflict(ctx context.Context, conflict *domainmemory.Conflict) (int64, error)
	GetConflict(ctx context.Context, id int64) (*domainmemory.Conflict, error)
	ListConflicts(ctx context.Context, pendingOnly bool) ([]*domainmemory.Conflict, error)
	ResolveConflict(ctx context.Context, id int64, resolution string, at time.Time) error
}

// Transaction exposes one transactional memory repository.
type Transaction interface{ Memory() Repository }

// UnitOfWork is the memory transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(context.Context, Transaction) error) error
}

// Candidate is one piece of durable information extracted by a model.
type Candidate struct {
	Title      string
	Content    string
	Category   domainmemory.Category
	Certainty  domainmemory.Certainty
	Confidence float64
	Entities   []string
	Evidence   string
	Correction bool
}

// Extractor learns from source material. Implementations live in the agent
// layer; an unavailable extractor never prevents manual memory or document
// indexing from working.
type Extractor interface {
	ExtractConversation(ctx context.Context, text string) ([]Candidate, error)
	ExtractDocument(ctx context.Context, filename, text string) ([]Candidate, error)
}

// ConflictView includes both claims so every automatic decision is auditable.
type ConflictView struct {
	Conflict  *domainmemory.Conflict
	Existing  *domainmemory.Entry
	Candidate *domainmemory.Entry
}

// ImportResult describes a document indexed into reusable knowledge.
type ImportResult struct {
	Entry           *domainmemory.Entry
	Duplicate       bool
	Extracted       int
	Conflicts       int
	ExtractionError string
}
