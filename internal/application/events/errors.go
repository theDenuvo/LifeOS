package events

import (
	"errors"

	"github.com/theDenuvo/LifeOS/internal/domain/event"
)

// Application errors.
var (
	ErrEventNotFound    = errors.New("event not found")
	ErrVersionConflict  = errors.New("event version conflict")
	ErrInvalidDateRange = errors.New("from must be on or before to")
	ErrInvalidCalendar  = errors.New("unknown calendar")
)

// EventWithVersion pairs an event with its optimistic-locking token.
type EventWithVersion struct {
	Event   *event.Event
	Version int
}
