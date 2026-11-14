// Package event owns calendar events (meetings and appointments).
//
// Events are scheduled intervals. They are deliberately separate from tasks:
// a task has a deadline, an event has a start and an end, and the calendar read
// model combines both without copying data between them.
package event

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Validation errors.
var (
	ErrInvalidTitle       = errors.New("title is required and must be 1-200 characters")
	ErrInvalidDescription = errors.New("description must be valid UTF-8 of at most 2000 characters")
	ErrInvalidLocation    = errors.New("location must be valid UTF-8 of at most 200 characters")
	ErrInvalidCalendar    = errors.New("calendar must be work, meetings, or personal")
	ErrInvalidTimezone    = errors.New("invalid timezone")
	ErrInvalidRange       = errors.New("ends_at must be after starts_at")
	ErrRangeTooLong       = errors.New("event must not be longer than 31 days")
)

// Domain string limits, in Unicode code points.
const (
	maxTitleLength       = 200
	maxDescriptionLength = 2000
	maxLocationLength    = 200
	maxDuration          = 31 * 24 * time.Hour
)

// Calendar identifies the source calendar an event belongs to.
type Calendar string

const (
	CalendarWork     Calendar = "work"
	CalendarMeetings Calendar = "meetings"
	CalendarPersonal Calendar = "personal"
)

// IsValid reports whether the calendar is known.
func (c Calendar) IsValid() bool {
	switch c {
	case CalendarWork, CalendarMeetings, CalendarPersonal:
		return true
	default:
		return false
	}
}

// Event is a scheduled calendar entry.
type Event struct {
	ID          int64
	Title       string
	Description *string
	Location    *string
	Calendar    Calendar
	StartsAt    time.Time // UTC
	EndsAt      time.Time // UTC
	AllDay      bool
	Timezone    string // IANA timezone used to interpret the wall-clock range
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewEvent validates and builds an event.
func NewEvent(
	title string,
	description *string,
	location *string,
	calendar Calendar,
	startsAt time.Time,
	endsAt time.Time,
	allDay bool,
	timezone string,
	now time.Time,
) (*Event, error) {
	createdAt := now.UTC()
	value := &Event{
		Title:       strings.TrimSpace(title),
		Description: cleanOptional(description),
		Location:    cleanOptional(location),
		Calendar:    calendar,
		StartsAt:    startsAt.UTC(),
		EndsAt:      endsAt.UTC(),
		AllDay:      allDay,
		Timezone:    timezone,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return value, nil
}

// Validate checks the event invariants.
func (e *Event) Validate() error {
	title := strings.TrimSpace(e.Title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) == 0 || utf8.RuneCountInString(title) > maxTitleLength {
		return ErrInvalidTitle
	}
	if e.Description != nil {
		if !utf8.ValidString(*e.Description) || utf8.RuneCountInString(*e.Description) > maxDescriptionLength {
			return ErrInvalidDescription
		}
	}
	if e.Location != nil {
		if !utf8.ValidString(*e.Location) || utf8.RuneCountInString(*e.Location) > maxLocationLength {
			return ErrInvalidLocation
		}
	}
	if !e.Calendar.IsValid() {
		return ErrInvalidCalendar
	}
	if e.Timezone == "" {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(e.Timezone); err != nil {
		return ErrInvalidTimezone
	}
	if e.StartsAt.IsZero() || e.EndsAt.IsZero() {
		return ErrInvalidRange
	}
	duration := e.EndsAt.Sub(e.StartsAt)
	if duration <= 0 {
		return ErrInvalidRange
	}
	if duration > maxDuration {
		return ErrRangeTooLong
	}
	return nil
}

func cleanOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
