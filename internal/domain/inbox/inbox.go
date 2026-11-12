// Package inbox owns the quick-capture entity: a raw thought, task or link
// before it is structured into a task.
package inbox

import (
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// Validation errors.
var (
	// ErrInvalidText is returned when the captured text is empty or too long.
	ErrInvalidText = errors.New("text is required and must be 1-2000 characters")

	// ErrInvalidMeta is returned when meta is not valid JSON.
	ErrInvalidMeta = errors.New("meta must be valid JSON")
)

const (
	maxTextLength = 2000
)

// Item is one captured inbox entry.
type Item struct {
	ID              int64
	Text            string
	Meta            json.RawMessage
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
	return nil
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
