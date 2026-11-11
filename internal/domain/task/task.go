package task

import (
	"errors"
	"time"
)

// ErrInvalidTitle is returned when a task title is invalid.
var ErrInvalidTitle = errors.New("title is required and must be 1-500 characters")

// ErrInvalidNote is returned when a task note is too long.
var ErrInvalidNote = errors.New("note must be at most 5000 characters")

// ErrInvalidPriority is returned when a priority value is invalid.
var ErrInvalidPriority = errors.New("priority must be low, medium, or high")

// ErrInvalidStatus is returned when a status value is invalid.
var ErrInvalidStatus = errors.New("status must be open, completed, or cancelled")

// ErrInvalidTimezone is returned when a timezone is invalid.
var ErrInvalidTimezone = errors.New("invalid timezone")

// ErrInvalidDueAtValue is returned when due_at is before created_at.
var ErrInvalidDueAtValue = errors.New("due_at must be after created_at")

// ErrInvalidDate is returned when a date is invalid.
var ErrInvalidDate = errors.New("invalid date format")

// ErrInvalidPlannedDate is returned when planned_date is invalid.
var ErrInvalidPlannedDate = errors.New("planned_date must be a valid calendar date")

// ErrInvalidDueDate is returned when due_date is invalid.
var ErrInvalidDueDate = errors.New("due_date must be a valid calendar date")

// ErrInvalidDueAtFormat is returned when due_at is invalid.
var ErrInvalidDueAtFormat = errors.New("due_at must be a valid RFC3339 timestamp")

// ErrInvalidTag is returned when a tag is invalid.
var ErrInvalidTag = errors.New("tags must be at most 50 characters each")

// CalendarDate represents a calendar date without time (YYYY-MM-DD).
// It is stored as a time.Time with zero time component in the task's timezone.
type CalendarDate struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseCalendarDate parses a date string in YYYY-MM-DD format.
func ParseCalendarDate(s string) (CalendarDate, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return CalendarDate{}, ErrInvalidDate
	}
	return CalendarDate{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// String returns the date in YYYY-MM-DD format.
func (d CalendarDate) String() string {
	if d.Year == 0 {
		return ""
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

// IsZero returns true if the date is not set.
func (d CalendarDate) IsZero() bool {
	return d.Year == 0
}

// Time returns the time.Time at midnight in the given location.
func (d CalendarDate) Time(loc *time.Location) time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// EndOfDay returns the end of day (23:59:59) in the given location.
func (d CalendarDate) EndOfDay(loc *time.Location) time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.Year, d.Month, d.Day, 23, 59, 59, 0, loc)
}

// Task represents the domain entity for a task.
type Task struct {
	ID           int64
	Title        string
	Note         *string
	Priority     Priority
	Status       Status
	PlannedDate  *CalendarDate
	DueDate      *CalendarDate
	DueAt        *time.Time // UTC
	Timezone     string     // IANA timezone
	Tags         []string
	CreatedAt    time.Time  // UTC
	UpdatedAt    time.Time  // UTC
	CompletedAt  *time.Time // UTC
	CancelledAt  *time.Time // UTC
	ParentTaskID *int64
}

// NewTask creates a new task with default values.
func NewTask(title string, priority Priority, timezone string) (*Task, error) {
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if !priority.IsValid() {
		return nil, ErrInvalidPriority
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, ErrInvalidTimezone
	}
	_ = loc // validated

	now := time.Now().UTC()
	return &Task{
		Title:     title,
		Priority:  priority,
		Status:    StatusOpen,
		Timezone:  timezone,
		Tags:      []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// TaskOption is a functional option for configuring a task.
type TaskOption func(*Task) error

// WithNote sets the note.
func WithNote(note string) TaskOption {
	return func(t *Task) error {
		if len(note) > 5000 {
			return ErrInvalidNote
		}
		t.Note = &note
		return nil
	}
}

// WithPlannedDate sets the planned date.
func WithPlannedDate(date CalendarDate) TaskOption {
	return func(t *Task) error {
		if !date.IsZero() {
			if _, err := time.LoadLocation(t.Timezone); err != nil {
				return ErrInvalidTimezone
			}
		}
		t.PlannedDate = &date
		return nil
	}
}

// WithDueDate sets the due date.
func WithDueDate(date CalendarDate) TaskOption {
	return func(t *Task) error {
		if !date.IsZero() {
			if _, err := time.LoadLocation(t.Timezone); err != nil {
				return ErrInvalidTimezone
			}
		}
		t.DueDate = &date
		return nil
	}
}

// WithDueAt sets the exact due timestamp.
func WithDueAt(dueAt time.Time) TaskOption {
	return func(t *Task) error {
		if !dueAt.IsZero() {
			if dueAt.Before(t.CreatedAt) {
				return ErrInvalidDueAtValue
			}
			t.DueAt = &dueAt
		}
		return nil
	}
}

// WithTimezone sets the timezone.
func WithTimezone(tz string) TaskOption {
	return func(t *Task) error {
		if _, err := time.LoadLocation(tz); err != nil {
			return ErrInvalidTimezone
		}
		t.Timezone = tz
		return nil
	}
}

// WithTags sets the tags.
func WithTags(tags []string) TaskOption {
	return func(t *Task) error {
		for _, tag := range tags {
			if len(tag) > 50 {
				return ErrInvalidTag
			}
		}
		t.Tags = tags
		return nil
	}
}

// WithParentTaskID sets the parent task ID.
func WithParentTaskID(id int64) TaskOption {
	return func(t *Task) error {
		t.ParentTaskID = &id
		return nil
	}
}

// Validate checks if the task is valid.
func (t *Task) Validate() error {
	if err := validateTitle(t.Title); err != nil {
		return err
	}
	if !t.Priority.IsValid() {
		return ErrInvalidPriority
	}
	if !t.Status.IsValid() {
		return ErrInvalidStatus
	}
	if _, err := time.LoadLocation(t.Timezone); err != nil {
		return ErrInvalidTimezone
	}
	if t.DueAt != nil && t.DueAt.Before(t.CreatedAt) {
		return ErrInvalidDueAtValue
	}
	for _, tag := range t.Tags {
		if len(tag) > 50 {
			return ErrInvalidTag
		}
	}
	return nil
}

// validateTitle checks if the title is valid.
func validateTitle(title string) error {
	if len(title) == 0 || len(title) > 500 {
		return ErrInvalidTitle
	}
	return nil
}

// Location returns the task's timezone location.
func (t *Task) Location() (*time.Location, error) {
	return time.LoadLocation(t.Timezone)
}

// IsOverdue determines if the task is overdue at the given time.
func (t *Task) IsOverdue(now time.Time) bool {
	if t.Status != StatusOpen {
		return false
	}
	if t.DueAt != nil {
		return now.After(*t.DueAt)
	}
	if t.DueDate != nil {
		loc, err := t.Location()
		if err != nil {
			return false
		}
		endOfDay := t.DueDate.EndOfDay(loc)
		return now.After(endOfDay)
	}
	return false
}

// Complete marks the task as completed.
func (t *Task) Complete(now time.Time) error {
	if err := t.Status.TransitionError(StatusCompleted); err != nil {
		return err
	}
	t.Status = StatusCompleted
	nowUTC := now.UTC()
	t.CompletedAt = &nowUTC
	t.UpdatedAt = nowUTC
	return nil
}

// Cancel marks the task as cancelled.
func (t *Task) Cancel(now time.Time) error {
	if err := t.Status.TransitionError(StatusCancelled); err != nil {
		return err
	}
	t.Status = StatusCancelled
	nowUTC := now.UTC()
	t.CancelledAt = &nowUTC
	t.UpdatedAt = nowUTC
	return nil
}

// Restore restores the task to open status.
func (t *Task) Restore(now time.Time) error {
	if err := t.Status.TransitionError(StatusOpen); err != nil {
		return err
	}
	t.Status = StatusOpen
	t.CompletedAt = nil
	t.CancelledAt = nil
	t.UpdatedAt = now.UTC()
	return nil
}

// Reschedule updates the task's scheduling fields.
func (t *Task) Reschedule(now time.Time, opts ...TaskOption) error {
	if t.Status != StatusOpen {
		return &InvalidTransitionError{From: t.Status, To: t.Status}
	}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return err
		}
	}
	t.UpdatedAt = now.UTC()
	return t.Validate()
}

// Update updates the task's mutable fields.
func (t *Task) Update(now time.Time, opts ...TaskOption) error {
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return err
		}
	}
	t.UpdatedAt = now.UTC()
	return t.Validate()
}

// ToEvent creates a TaskEvent for the given change.
func (t *Task) ToEvent(eventType EventType, payload interface{}, actor Actor) (*TaskEvent, error) {
	return NewTaskEvent(t.ID, eventType, payload, actor)
}

// OverdueAt returns the time when the task becomes overdue, if applicable.
func (t *Task) OverdueAt() *time.Time {
	if t.DueAt != nil {
		return t.DueAt
	}
	if t.DueDate != nil {
		loc, err := t.Location()
		if err != nil {
			return nil
		}
		endOfDay := t.DueDate.EndOfDay(loc)
		return &endOfDay
	}
	return nil
}
