// Package inbox owns the quick-capture entity: a raw thought, task or link
// before it is structured into a task.
package inbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Validation errors.
var (
	// ErrInvalidText is returned when the captured text is empty or too long.
	ErrInvalidText = errors.New("text is required and must be 1-2000 characters")

	// ErrInvalidMeta is returned when meta is not valid JSON.
	ErrInvalidMeta = errors.New("meta must be valid JSON")

	// ErrInvalidKind is returned when the captured kind is unknown.
	ErrInvalidKind = errors.New("kind must be task, idea, note, or goal")
)

// Kind says what was captured.
//
// Not everything a person writes down is a task. An idea or a note is kept as
// captured and revisited later instead of being forced into the task list.
type Kind string

// Captured kinds.
const (
	KindTask Kind = "task"
	KindIdea Kind = "idea"
	KindNote Kind = "note"
	KindGoal Kind = "goal"
)

// IsValid reports whether the kind is known.
func (k Kind) IsValid() bool {
	switch k {
	case KindTask, KindIdea, KindNote, KindGoal:
		return true
	default:
		return false
	}
}

// RevisitLater reports whether the kind is returned to the user on a date rather
// than worked on immediately.
func (k Kind) RevisitLater() bool { return k == KindIdea || k == KindNote }

const (
	maxTextLength = 2000
)

// Item is one captured inbox entry.
type Item struct {
	ID              int64
	Text            string
	Meta            json.RawMessage
	Kind            Kind
	RevisitAt       *task.CalendarDate
	CreatedAt       time.Time
	ProcessedAt     *time.Time
	ConvertedTaskID *int64
}

// NewItem validates and builds a new inbox item.
//
// The creation timestamp is supplied by the caller so the domain stays
// deterministic, exactly like the task aggregate.
func NewItem(text string, meta json.RawMessage, createdAt time.Time) (*Item, error) {
	item := &Item{
		Text:      text,
		Meta:      meta,
		Kind:      KindTask,
		CreatedAt: createdAt.UTC(),
	}
	if err := item.Validate(); err != nil {
		return nil, err
	}
	return item, nil
}

// Validate checks the captured text and metadata.
func (i *Item) Validate() error {
	if !utf8.ValidString(i.Text) {
		return ErrInvalidText
	}
	length := utf8.RuneCountInString(i.Text)
	if length == 0 || length > maxTextLength {
		return ErrInvalidText
	}
	if len(i.Meta) > 0 && !json.Valid(i.Meta) {
		return ErrInvalidMeta
	}
	if i.Kind == "" {
		i.Kind = KindTask
	}
	if !i.Kind.IsValid() {
		return ErrInvalidKind
	}
	if i.RevisitAt != nil && !i.RevisitAt.IsZero() && !i.RevisitAt.IsValid() {
		return fmt.Errorf("%w: revisit_at must be a calendar date", task.ErrInvalidDate)
	}
	return nil
}

// SetKind changes what the capture is.
func (i *Item) SetKind(kind Kind) error {
	if !kind.IsValid() {
		return ErrInvalidKind
	}
	i.Kind = kind
	return nil
}

// SetRevisitAt schedules a later return to the capture. A zero date clears it.
func (i *Item) SetRevisitAt(date *task.CalendarDate) {
	if date == nil || date.IsZero() {
		i.RevisitAt = nil
		return
	}
	value := *date
	i.RevisitAt = &value
}

// IsProcessed reports whether the item was already converted or dismissed.
func (i *Item) IsProcessed() bool {
	return i.ProcessedAt != nil
}

// MarkProcessed records that the item became a task.
func (i *Item) MarkProcessed(at time.Time, taskID int64) {
	processedAt := at.UTC()
	i.ProcessedAt = &processedAt
	i.ConvertedTaskID = &taskID
}
