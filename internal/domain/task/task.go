package task

import (
	"errors"
	"time"
	"unicode/utf8"
)

// Domain validation errors.
//
// String length limits are counted in Unicode code points, not bytes, so that
// Cyrillic and other multi-byte text is measured the way the documentation
// describes it.
var (
	// ErrInvalidTitle is returned when a task title is invalid.
	ErrInvalidTitle = errors.New("title is required and must be 1-500 characters")

	// ErrInvalidNote is returned when a task note is invalid.
	ErrInvalidNote = errors.New("note must be valid UTF-8 of at most 5000 characters")

	// ErrInvalidPriority is returned when a priority value is invalid.
	ErrInvalidPriority = errors.New("priority must be low, medium, or high")

	// ErrInvalidStatus is returned when a status value is invalid.
	ErrInvalidStatus = errors.New("status must be open, completed, or cancelled")

	// ErrInvalidTimezone is returned when a timezone is invalid.
	ErrInvalidTimezone = errors.New("invalid timezone")

	// ErrInvalidDate is returned when a calendar date value is invalid.
	ErrInvalidDate = errors.New("invalid date format")

	// ErrInvalidPlannedDate is returned when planned_date is invalid.
	ErrInvalidPlannedDate = errors.New("planned_date must be a valid calendar date")

	// ErrInvalidDueDate is returned when due_date is invalid.
	ErrInvalidDueDate = errors.New("due_date must be a valid calendar date")

	// ErrInvalidDueAtFormat is returned when due_at is not a valid timestamp.
	ErrInvalidDueAtFormat = errors.New("due_at must be a valid RFC3339 timestamp")

	// ErrInvalidTag is returned when a tag is invalid.
	ErrInvalidTag = errors.New("tags must be valid UTF-8 of at most 50 characters each")

	// ErrInvalidEventType is returned when an event type is invalid.
	ErrInvalidEventType = errors.New("invalid event type")

	// ErrInvalidActor is returned when an actor is invalid.
	ErrInvalidActor = errors.New("invalid actor")
)

// Domain string limits, expressed in Unicode code points.
const (
	maxTitleLength = 500
	maxNoteLength  = 5000
	maxTagLength   = 50
)

// Task represents the domain entity for a task.
type Task struct {
	ID           int64
	Title        string
	Note         *string
	Priority     Priority
	Status       Status
	PlannedDate  *CalendarDate
	DueDate      *CalendarDate
	DueAt        *time.Time // exact instant, UTC
	Timezone     string     // IANA timezone
	Tags         []string
	CreatedAt    time.Time  // UTC
	UpdatedAt    time.Time  // UTC
	CompletedAt  *time.Time // UTC
	CancelledAt  *time.Time // UTC
	ParentTaskID *int64
}

// NewTask creates a new task with default values.
//
// The creation timestamp is supplied by the caller so that the domain stays
// deterministic: the Application layer decides what "now" is and can use the
// same instant for the task and for its first event.
func NewTask(title string, priority Priority, timezone string, createdAt time.Time) (*Task, error) {
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if !priority.IsValid() {
		return nil, ErrInvalidPriority
	}
	if !validTimezone(timezone) {
		return nil, ErrInvalidTimezone
	}
	now := createdAt.UTC()
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

// WithTitle sets the title.
func WithTitle(title string) TaskOption {
	return func(t *Task) error {
		if err := validateTitle(title); err != nil {
			return err
		}
		t.Title = title
		return nil
	}
}

// WithNote sets the note.
func WithNote(note string) TaskOption {
	return func(t *Task) error {
		if !utf8.ValidString(note) {
			return ErrInvalidNote
		}
		if utf8.RuneCountInString(note) > maxNoteLength {
			return ErrInvalidNote
		}
		t.Note = &note
		return nil
	}
}

// WithPlannedDate sets the planned date.
func WithPlannedDate(date CalendarDate) TaskOption {
	return func(t *Task) error {
		if date.IsZero() {
			t.PlannedDate = &date
			return nil
		}
		if !validTimezone(t.Timezone) {
			return ErrInvalidTimezone
		}
		if !date.IsValid() {
			return ErrInvalidPlannedDate
		}
		t.PlannedDate = &date
		return nil
	}
}

// WithDueDate sets the due date.
func WithDueDate(date CalendarDate) TaskOption {
	return func(t *Task) error {
		if date.IsZero() {
			t.DueDate = &date
			return nil
		}
		if !validTimezone(t.Timezone) {
			return ErrInvalidTimezone
		}
		if !date.IsValid() {
			return ErrInvalidDueDate
		}
		t.DueDate = &date
		return nil
	}
}

// WithDueAt sets the exact due timestamp.
//
// A due timestamp in the past is allowed: an overdue deadline is a normal state,
// not a validation error. Overdue-ness is derived from IsOverdue.
func WithDueAt(dueAt time.Time) TaskOption {
	return func(t *Task) error {
		if dueAt.IsZero() {
			return ErrInvalidDueAtFormat
		}
		value := dueAt.UTC()
		t.DueAt = &value
		return nil
	}
}

// WithTimezone sets the timezone.
func WithTimezone(tz string) TaskOption {
	return func(t *Task) error {
		if !validTimezone(tz) {
			return ErrInvalidTimezone
		}
		t.Timezone = tz
		return nil
	}
}

// WithTags replaces the tags with a private copy of the given slice.
func WithTags(tags []string) TaskOption {
	return func(t *Task) error {
		for _, tag := range tags {
			if err := validateTag(tag); err != nil {
				return err
			}
		}
		// Copy so that later mutation of the caller's slice cannot change the task.
		stored := make([]string, len(tags))
		for i, tag := range tags {
			stored[i] = tag
		}
		t.Tags = stored
		return nil
	}
}

// WithParentTaskID sets the parent task ID.
func WithParentTaskID(id int64) TaskOption {
	return func(t *Task) error {
		value := id
		t.ParentTaskID = &value
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
	if !validTimezone(t.Timezone) {
		return ErrInvalidTimezone
	}
	if t.Note != nil {
		if !utf8.ValidString(*t.Note) || utf8.RuneCountInString(*t.Note) > maxNoteLength {
			return ErrInvalidNote
		}
	}
	if t.PlannedDate != nil && !t.PlannedDate.IsValid() {
		return ErrInvalidPlannedDate
	}
	if t.DueDate != nil && !t.DueDate.IsValid() {
		return ErrInvalidDueDate
	}
	for _, tag := range t.Tags {
		if err := validateTag(tag); err != nil {
			return err
		}
	}
	return nil
}

// Location returns the task's timezone location.
func (t *Task) Location() (*time.Location, error) {
	return time.LoadLocation(t.Timezone)
}

// OverdueAt returns the instant at which the task becomes overdue, or nil when
// the task has no deadline.
//
// An exact due timestamp is returned as-is. A calendar due date yields the local
// midnight that starts the following calendar day, so the whole local day is
// still allowed.
//
// Note that the boundary is compared differently per deadline type, see
// IsOverdue: for an exact timestamp the boundary itself is still on time, for a
// calendar date the boundary is already overdue.
func (t *Task) OverdueAt() *time.Time {
	if t.DueAt != nil {
		dueAt := t.DueAt.UTC()
		return &dueAt
	}
	if t.DueDate != nil {
		loc, err := t.Location()
		if err != nil {
			return nil
		}
		boundary := t.DueDate.EndOfDay(loc)
		if boundary.IsZero() {
			return nil
		}
		boundaryUTC := boundary.UTC()
		return &boundaryUTC
	}
	return nil
}

// IsOverdue determines whether the task is overdue at the given time.
//
// Completed and cancelled tasks are never overdue.
//
// The two deadline types deliberately use different comparisons:
//
//   - Exact timestamp (DueAt): strict comparison. The task is overdue only
//     strictly after the deadline, so at exactly DueAt it is still on time.
//   - Calendar date (DueDate): inclusive boundary. The whole local calendar day
//     is allowed, and the task is overdue from the start of the next local day
//     (next local midnight) onwards, so at exactly that midnight it is overdue.
//
// When both are set, DueAt wins.
func (t *Task) IsOverdue(now time.Time) bool {
	if t.Status != StatusOpen {
		return false
	}
	if t.DueAt != nil {
		return now.After(*t.DueAt)
	}
	if t.DueDate != nil {
		boundary := t.OverdueAt()
		if boundary == nil {
			return false
		}
		return !now.Before(*boundary)
	}
	return false
}

// Complete marks the task as completed.
func (t *Task) Complete(now time.Time) error {
	if err := t.Status.TransitionError(StatusCompleted); err != nil {
		return err
	}
	completedAt := now.UTC()
	t.Status = StatusCompleted
	t.CompletedAt = &completedAt
	t.CancelledAt = nil
	t.UpdatedAt = completedAt
	return nil
}

// Cancel marks the task as cancelled.
func (t *Task) Cancel(now time.Time) error {
	if err := t.Status.TransitionError(StatusCancelled); err != nil {
		return err
	}
	cancelledAt := now.UTC()
	t.Status = StatusCancelled
	t.CancelledAt = &cancelledAt
	t.CompletedAt = nil
	t.UpdatedAt = cancelledAt
	return nil
}

// Restore restores the task to open status.
func (t *Task) Restore(now time.Time) error {
	if err := t.Status.TransitionError(StatusOpen); err != nil {
		return err
	}
	restoredAt := now.UTC()
	t.Status = StatusOpen
	t.CompletedAt = nil
	t.CancelledAt = nil
	t.UpdatedAt = restoredAt
	return nil
}

// Update applies the given options atomically.
//
// The options are applied to a deep copy first. The aggregate is only replaced
// when every option succeeds and the resulting copy is valid, so a failed update
// leaves every field, pointer and slice of the original task untouched,
// including UpdatedAt.
func (t *Task) Update(now time.Time, opts ...TaskOption) error {
	candidate := t.clone()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(candidate); err != nil {
			return err
		}
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	candidate.UpdatedAt = now.UTC()
	*t = *candidate
	return nil
}

// Reschedule updates the task's scheduling fields atomically.
//
// Rescheduling is only allowed while the task is open; it follows the same
// all-or-nothing rule as Update.
func (t *Task) Reschedule(now time.Time, opts ...TaskOption) error {
	if t.Status != StatusOpen {
		return &InvalidTransitionError{From: t.Status, To: StatusOpen}
	}
	return t.Update(now, opts...)
}

// ToEvent creates a TaskEvent for the given change.
//
// The event timestamp is supplied by the caller so that one Application
// operation can use a single instant for both the aggregate change and its event.
func (t *Task) ToEvent(eventType EventType, payload interface{}, actor Actor, createdAt time.Time) (*TaskEvent, error) {
	return NewTaskEvent(t.ID, eventType, payload, actor, createdAt)
}

// clone returns a deep copy of the task with no shared mutable state.
func (t *Task) clone() *Task {
	copied := *t

	if t.Note != nil {
		note := *t.Note
		copied.Note = &note
	}
	if t.DueAt != nil {
		dueAt := *t.DueAt
		copied.DueAt = &dueAt
	}
	if t.CompletedAt != nil {
		completedAt := *t.CompletedAt
		copied.CompletedAt = &completedAt
	}
	if t.CancelledAt != nil {
		cancelledAt := *t.CancelledAt
		copied.CancelledAt = &cancelledAt
	}
	if t.PlannedDate != nil {
		planned := *t.PlannedDate
		copied.PlannedDate = &planned
	}
	if t.DueDate != nil {
		due := *t.DueDate
		copied.DueDate = &due
	}
	if t.ParentTaskID != nil {
		parent := *t.ParentTaskID
		copied.ParentTaskID = &parent
	}
	if t.Tags != nil {
		tags := make([]string, len(t.Tags))
		for i, tag := range t.Tags {
			tags[i] = tag
		}
		copied.Tags = tags
	}

	return &copied
}

// validTimezone reports whether tz is an IANA timezone known to the runtime.
func validTimezone(tz string) bool {
	if tz == "" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// validateTitle checks the title against the documented limits.
func validateTitle(title string) error {
	if !utf8.ValidString(title) {
		return ErrInvalidTitle
	}
	length := utf8.RuneCountInString(title)
	if length == 0 || length > maxTitleLength {
		return ErrInvalidTitle
	}
	return nil
}

// validateTag checks a single tag against the documented limits.
func validateTag(tag string) error {
	if !utf8.ValidString(tag) {
		return ErrInvalidTag
	}
	if utf8.RuneCountInString(tag) > maxTagLength {
		return ErrInvalidTag
	}
	return nil
}
