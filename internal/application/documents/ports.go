package documents

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Application errors.
var (
	ErrDocumentNotFound = errors.New("document not found")
	ErrFileTooLarge     = errors.New("file is too large")
	ErrUnsupportedFile  = errors.New("unsupported file type")
	ErrExtractionFailed = errors.New("document extraction failed")
	ErrLLMNotConfigured = errors.New("document extraction model is not configured")
	ErrNoTasks          = errors.New("no task proposals")
)

// StoredFile is the result of persisting an upload.
type StoredFile struct {
	Path   string
	Size   int64
	SHA256 string
}

// FileStore persists uploaded source files.
type FileStore interface {
	Save(filename string, reader io.Reader, maxBytes int64) (StoredFile, error)
	Delete(path string) error
}

// Extraction is the local, deterministic part of document processing.
type Extraction struct {
	Text      string
	Image     []byte
	ImageMIME string
}

// Extractor reads text or image data from a file.
type Extractor interface {
	Extract(ctx context.Context, path, filename, mime string) (Extraction, error)
}

// ExtractedTask is the unvalidated task candidate returned by the model.
type ExtractedTask struct {
	Title      string  `json:"title"`
	Note       *string `json:"note"`
	DueDate    *string `json:"due_date"`
	Priority   string  `json:"priority"`
	Confidence float64 `json:"confidence"`
	Source     *string `json:"source"`
}

// TaskExtractionInput is one model extraction request.
type TaskExtractionInput struct {
	Filename  string
	Text      string
	Image     []byte
	ImageMIME string
	Today     time.Time
	Timezone  string
}

// MarkdownConverter turns an image or extracted text into Markdown.
type MarkdownConverter interface {
	ConvertToMarkdown(ctx context.Context, input TaskExtractionInput) (string, error)
}

// TaskCreator is the task-service slice needed to accept proposals.
type TaskCreator interface {
	CreateTask(ctx context.Context, in tasks.CreateTaskInput, actor task.Actor) (tasks.TaskWithVersion, error)
}

// Repository persists documents and proposals.
type Repository interface {
	CreateDocument(ctx context.Context, value *document.Document) (int64, error)
	GetDocument(ctx context.Context, id int64) (*document.Document, error)
	ListDocuments(ctx context.Context, limit, offset int) ([]*document.Document, int, error)
	SetDocumentStatus(ctx context.Context, id int64, status document.Status, errorText *string, extractedText *string, at time.Time) error
	ReplaceProposals(ctx context.Context, documentID int64, proposals []*document.ProposedTask, extractedText string, at time.Time) error
	ListProposals(ctx context.Context, documentID int64) ([]*document.ProposedTask, error)
	GetProposal(ctx context.Context, id int64) (*document.ProposedTask, error)
	MarkProposalAccepted(ctx context.Context, id, taskID int64, at time.Time) error
	MarkProposalDismissed(ctx context.Context, id int64, at time.Time) error
	DeleteDocument(ctx context.Context, id int64) error
}

// Transaction exposes repositories bound to one transaction.
type Transaction interface {
	Documents() Repository
}

// UnitOfWork defines the document transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}

// View is a document with its proposals.
type View struct {
	Document *document.Document
	Tasks    []*document.ProposedTask
}
