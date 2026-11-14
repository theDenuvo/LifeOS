// Package document owns uploaded source documents and the task proposals
// extracted from them. A proposal is not a task: it becomes one only after the
// user explicitly accepts it.
package document

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Document processing status.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusProcessed  Status = "processed"
	StatusFailed     Status = "failed"
)

// IsValid reports whether the status is known.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusProcessing, StatusProcessed, StatusFailed:
		return true
	default:
		return false
	}
}

// TaskStatus is the lifecycle of one extracted proposal.
type TaskStatus string

const (
	TaskProposed  TaskStatus = "proposed"
	TaskAccepted  TaskStatus = "accepted"
	TaskDismissed TaskStatus = "dismissed"
)

// IsValid reports whether the proposal status is known.
func (s TaskStatus) IsValid() bool {
	switch s {
	case TaskProposed, TaskAccepted, TaskDismissed:
		return true
	default:
		return false
	}
}

// Document is one uploaded source file.
type Document struct {
	ID            int64
	Filename      string
	MIME          string
	Size          int64
	SHA256        string
	Path          string
	Status        Status
	Error         *string
	ExtractedText *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProposedTask is a task candidate found in a document.
type ProposedTask struct {
	ID         int64
	DocumentID int64
	Title      string
	Note       *string
	DueDate    *task.CalendarDate
	Priority   task.Priority
	Confidence float64
	Source     *string
	Status     TaskStatus
	TaskID     *int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Validation errors.
var (
	ErrInvalidTitle      = errors.New("proposal title is required and must be 1-500 characters")
	ErrInvalidNote       = errors.New("proposal note must be at most 5000 characters")
	ErrInvalidPriority   = errors.New("proposal priority must be low, medium or high")
	ErrInvalidConfidence = errors.New("proposal confidence must be between 0 and 1")
	ErrInvalidStatus     = errors.New("invalid proposal status")
)

// NewProposedTask validates and builds a proposal.
func NewProposedTask(documentID int64, title string, note *string, dueDate *task.CalendarDate, priority task.Priority, confidence float64, source *string, now time.Time) (*ProposedTask, error) {
	trimmed := strings.TrimSpace(title)
	if !utf8.ValidString(trimmed) || utf8.RuneCountInString(trimmed) == 0 || utf8.RuneCountInString(trimmed) > 500 {
		return nil, ErrInvalidTitle
	}
	if note != nil {
		value := strings.TrimSpace(*note)
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 5000 {
			return nil, ErrInvalidNote
		}
		if value == "" {
			note = nil
		} else {
			note = &value
		}
	}
	if !priority.IsValid() {
		return nil, ErrInvalidPriority
	}
	if confidence < 0 || confidence > 1 {
		return nil, ErrInvalidConfidence
	}
	if source != nil {
		value := strings.TrimSpace(*source)
		if value == "" {
			source = nil
		} else {
			source = &value
		}
	}
	return &ProposedTask{
		DocumentID: documentID,
		Title:      trimmed,
		Note:       note,
		DueDate:    dueDate,
		Priority:   priority,
		Confidence: confidence,
		Source:     source,
		Status:     TaskProposed,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}, nil
}
