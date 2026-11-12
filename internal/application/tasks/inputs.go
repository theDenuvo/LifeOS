package tasks

import (
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Optional distinguishes "field absent" from "field present with the zero
// value". A plain string or *T cannot: an empty string could mean either "not
// supplied" or "clear this field". Nullable task fields (note, planned_date,
// due_date, due_at) therefore use Optional so a caller can clear them
// explicitly.
type Optional[T any] struct {
	value T
	set   bool
}

// Some returns an Optional holding value.
func Some[T any](value T) Optional[T] {
	return Optional[T]{value: value, set: true}
}

// None returns an Optional with no value, meaning "leave unchanged".
func None[T any]() Optional[T] {
	return Optional[T]{}
}

// IsSet reports whether a value was supplied.
func (o Optional[T]) IsSet() bool { return o.set }

// Get returns the value and whether it was set.
func (o Optional[T]) Get() (T, bool) { return o.value, o.set }

// Or returns the value when set, otherwise fallback.
func (o Optional[T]) Or(fallback T) T {
	if o.set {
		return o.value
	}
	return fallback
}

// CreateTaskInput is the typed input for CreateTask.
//
// Zero values mean "use the default": empty Priority falls back to the service
// default priority, empty Timezone to the configured default timezone. All other
// fields are optional.
type CreateTaskInput struct {
	Title       string
	Note        Optional[string]
	Priority    Optional[task.Priority]
	PlannedDate Optional[task.CalendarDate]
	DueDate     Optional[task.CalendarDate]
	DueAt       Optional[time.Time]
	Timezone    Optional[string]
	Tags        []string
}

// UpdateTaskInput is the typed input for UpdateTask.
//
// Every field is an Optional: an unset field is left untouched, a set field is
// applied. Clearing is explicit — Some("") clears a note, Some(task.CalendarDate{})
// clears a date, Some([]string{}) clears the tags.
type UpdateTaskInput struct {
	Title       Optional[string]
	Note        Optional[string]
	Priority    Optional[task.Priority]
	PlannedDate Optional[task.CalendarDate]
	DueDate     Optional[task.CalendarDate]
	DueAt       Optional[time.Time]
	Timezone    Optional[string]
	Tags        Optional[[]string]
}

// anySet reports whether the update request explicitly supplies at least one
// field. An entirely empty patch is a read-only no-op.
func (in UpdateTaskInput) anySet() bool {
	return in.Title.IsSet() ||
		in.Note.IsSet() ||
		in.Priority.IsSet() ||
		in.PlannedDate.IsSet() ||
		in.DueDate.IsSet() ||
		in.DueAt.IsSet() ||
		in.Timezone.IsSet() ||
		in.Tags.IsSet()
}

// RescheduleTaskInput carries the scheduling fields for RescheduleTask.
//
// Clearing follows the same rule as UpdateTask: Some(task.CalendarDate{}) clears
// a calendar deadline and Some(time.Time{}) clears the exact deadline.
type RescheduleTaskInput struct {
	PlannedDate Optional[task.CalendarDate]
	DueDate     Optional[task.CalendarDate]
	DueAt       Optional[time.Time]
}

// anySet reports whether the request carries at least one field.
func (in RescheduleTaskInput) anySet() bool {
	return in.PlannedDate.IsSet() || in.DueDate.IsSet() || in.DueAt.IsSet()
}

// TaskSortField is a permitted sort column.
type TaskSortField string

// Permitted sort fields, from API_CONTRACT.md.
const (
	SortCreatedAt TaskSortField = "created_at"
	SortUpdatedAt TaskSortField = "updated_at"
	SortDueAt     TaskSortField = "due_at"
	SortPriority  TaskSortField = "priority"
	SortTitle     TaskSortField = "title"
)

// SortOrder is the direction of a sort.
type SortOrder string

// Sort directions.
const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// TaskSort is one parsed sort clause.
type TaskSort struct {
	Field TaskSortField
	Order SortOrder
}

// TaskFilter is the typed filter for ListTasks.
//
// It contains Go and domain types only — no JSON, no raw query strings. HTTP
// parsing happens in the transport layer.
type TaskFilter struct {
	Statuses    []task.Status
	Priorities  []task.Priority
	PlannedDate Optional[task.CalendarDate]
	DueDate     Optional[task.CalendarDate]
	Overdue     Optional[bool]
	Search      Optional[string]
	Tags        []string
	Sorts       []TaskSort
	Limit       Optional[int]
	Offset      Optional[int]

	// AsOf is the reference instant used to evaluate the Overdue filter. It is
	// populated by Service.ListTasks from the injected Clock whenever Overdue is
	// set; a repository must use it rather than reading a clock of its own, so
	// overdue is deterministic and agrees with Task.IsOverdue. It is ignored when
	// Overdue is not set.
	AsOf Optional[time.Time]
}

// ListTasksResult is a page of tasks plus pagination metadata.
type ListTasksResult struct {
	Tasks  []TaskWithVersion
	Total  int
	Limit  int
	Offset int
}

// ParseSort parses the wire form of a sort parameter, e.g. "-due_at" for
// descending. The transport layer converts request strings into TaskSort before
// calling the application, but the rule itself lives here so it is applied
// uniformly.
func ParseSort(raw string) (TaskSort, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return TaskSort{}, ErrInvalidSort
	}

	order := SortAsc
	if strings.HasPrefix(value, "-") {
		order = SortDesc
		value = strings.TrimPrefix(value, "-")
	} else if strings.HasPrefix(value, "+") {
		value = strings.TrimPrefix(value, "+")
	}

	field := TaskSortField(value)
	switch field {
	case SortCreatedAt, SortUpdatedAt, SortDueAt, SortPriority, SortTitle:
		return TaskSort{Field: field, Order: order}, nil
	default:
		return TaskSort{}, ErrInvalidSort
	}
}
