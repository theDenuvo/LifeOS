// Package research turns a question into verified evidence: it searches the
// web, reads the pages, compares what they say, and keeps the statement only
// with its sources attached. The same engine re-asks a standing question on a
// cadence and reports what changed.
//
// The product rules it enforces: an external service is an addition, never the
// place where LifeOS keeps its data; every source has an explicit permission
// (read, write, sync) that the user granted; and the assistant may read and
// store what it learned, while outbound changes stay a confirmed decision.
package research

import (
	"context"
	"errors"
	"time"

	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// Errors shared by the service and the transports.
var (
	ErrNotFound          = errors.New("исследование не найдено")
	ErrSourceNotFound    = errors.New("источник не найден")
	ErrWatchNotFound     = errors.New("наблюдение не найдено")
	ErrVersionConflict   = errors.New("запись изменена другой операцией")
	ErrSourceDisabled    = errors.New("источник отключён пользователем")
	ErrSourceReadOnly    = errors.New("источнику разрешено только чтение")
	ErrNoProvider        = errors.New("поиск в интернете не настроен")
	ErrEmptyQuestion     = errors.New("нужен вопрос или хотя бы один запрос")
	ErrNoEvidence        = errors.New("источники не дали проверяемых выводов")
	ErrInvalidCall       = errors.New("запрос к внешнему источнику некорректен")
	ErrScriptNeedsUser   = errors.New("источник-скрипт создаёт только пользователь")
	ErrKnowledgeNotSaved = errors.New("результат не сохранён в базу знаний")
)

// Clock supplies timestamps to all mutations.
type Clock interface{ Now() time.Time }

// SearchResult is one page a search engine returned.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// Page is one fetched document, reduced to the text LifeOS can read.
type Page struct {
	URL       string
	Title     string
	Text      string
	FetchedAt time.Time
	// Truncated reports that the page was longer than the limit and only its
	// beginning was read.
	Truncated bool
}

// CallRequest is one outbound request to a connected source.
type CallRequest struct {
	// Operation is the path for http_api (`/repos/owner/name`), the tool name
	// for mcp, and is unused for web_search and script.
	Operation string
	// Method is the HTTP method; GET when empty.
	Method string
	// Body is the JSON payload to send.
	Body string
	// Query is the search phrase for web_search and the single argument for
	// script.
	Query string
	// Write marks a request that changes the remote service. A read-only
	// source refuses it.
	Write bool
}

// CallResult is what a connected source answered.
type CallResult struct {
	Status      int
	ContentType string
	Body        string
	Duration    time.Duration
}

// External is the transport to the outside world: the built-in web provider
// (connection == nil) and the user's connected sources.
type External interface {
	// Search queries a search endpoint. A nil connection means the built-in
	// provider configured for this LifeOS instance.
	Search(ctx context.Context, connection *domainresearch.Connection, query string, limit int) ([]SearchResult, error)
	// Fetch reads one page and extracts its readable text.
	Fetch(ctx context.Context, rawURL string) (Page, error)
	// Call talks to a connected source: an HTTP API path, an MCP tool or a
	// user-configured command.
	Call(ctx context.Context, connection *domainresearch.Connection, request CallRequest) (CallResult, error)
}

// KnowledgeInput is one researched result ready for the knowledge base.
type KnowledgeInput struct {
	Title         string
	Content       string
	Certainty     string
	Confidence    float64
	SourceLabel   string
	SourceURL     string
	SourceExcerpt string
}

// KnowledgeSink stores a finished research result as reusable knowledge with
// its provenance. It is a narrow port so research does not depend on the
// memory module's storage.
type KnowledgeSink interface {
	SaveResearch(ctx context.Context, input KnowledgeInput) (int64, error)
}

// ReportFilter bounds report listing.
type ReportFilter struct {
	Query  string
	Limit  int
	Offset int
}

// ConnectionFilter bounds source listing.
type ConnectionFilter struct {
	Enabled *bool
	Kind    *domainresearch.SourceKind
}

// CallRecord is one audited conversation with an external source.
type CallRecord struct {
	ID           int64
	ConnectionID int64
	Name         string
	Kind         domainresearch.SourceKind
	Mode         domainresearch.AccessMode
	Operation    string
	Outcome      string // ok | refused | failed
	Detail       string
	DurationMS   int64
	CreatedAt    time.Time
}

// Outcomes of an audited call.
const (
	CallOK      = "ok"
	CallRefused = "refused"
	CallFailed  = "failed"
)

// Repository is the persistence boundary of reports, sources and watches.
type Repository interface {
	InsertReport(ctx context.Context, report *domainresearch.Report) (int64, error)
	GetReport(ctx context.Context, id int64) (*domainresearch.Report, error)
	ListReports(ctx context.Context, filter ReportFilter) ([]*domainresearch.Report, int, error)
	// UpdateReport performs a conditional write: the write happens only when
	// the version still matches, and the row's version is incremented.
	UpdateReport(ctx context.Context, id int64, expectedVersion int, report *domainresearch.Report) (*domainresearch.Report, error)
	DeleteReport(ctx context.Context, id int64) error

	InsertConnection(ctx context.Context, connection *domainresearch.Connection) (int64, error)
	GetConnection(ctx context.Context, id int64) (*domainresearch.Connection, error)
	ListConnections(ctx context.Context, filter ConnectionFilter) ([]*domainresearch.Connection, error)
	UpdateConnection(ctx context.Context, id int64, expectedVersion int, connection *domainresearch.Connection) (*domainresearch.Connection, error)
	DeleteConnection(ctx context.Context, id int64) error

	InsertWatch(ctx context.Context, watch *domainresearch.Watch) (int64, error)
	GetWatch(ctx context.Context, id int64) (*domainresearch.Watch, error)
	ListWatches(ctx context.Context, includeDisabled bool) ([]*domainresearch.Watch, error)
	// DueWatches returns enabled watches whose next check has come or whose
	// bookmark is missing and needs recomputing.
	DueWatches(ctx context.Context, now time.Time) ([]*domainresearch.Watch, error)
	UpdateWatch(ctx context.Context, id int64, expectedVersion int, watch *domainresearch.Watch) (*domainresearch.Watch, error)
	DeleteWatch(ctx context.Context, id int64) error

	RecordCall(ctx context.Context, record *CallRecord) error
	ListCalls(ctx context.Context, connectionID int64, limit int) ([]*CallRecord, int, error)
}

// Transaction exposes one transactional research repository.
type Transaction interface{ Research() Repository }

// UnitOfWork is the research transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(context.Context, Transaction) error) error
}

// ResearchInput is one question to investigate.
type ResearchInput struct {
	Question string
	// Queries are the search phrases. When empty the question itself is used,
	// which is enough for a plain sentence.
	Queries []string
	// MaxPages bounds how many pages are read. Zero uses the default.
	MaxPages int
	// SaveKnowledge stores the result in the knowledge base with its sources.
	SaveKnowledge bool
	// KnowledgeTitle overrides the entry title; empty derives it from the
	// question.
	KnowledgeTitle string
	// ConnectionID forces one connected search source instead of the built-in
	// provider.
	ConnectionID int64
}

// WatchInput is a standing question to keep an eye on.
type WatchInput struct {
	Topic   string
	Query   string
	Queries []string
	Cadence domainresearch.Cadence
	Enabled *bool
}

// WatchUpdate is a partial edit of a watch.
type WatchUpdate struct {
	Topic   *string
	Query   *string
	Queries []string
	Cadence *domainresearch.Cadence
	Enabled *bool
}

// WatchTick summarises one runner pass over the due watches.
type WatchTick struct {
	Checked int
	Changed int
	Failed  int
}
