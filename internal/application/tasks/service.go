package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// DefaultPriority and DefaultTimezone are the product defaults for new tasks.
const (
	DefaultPriority = task.PriorityMedium
	DefaultTimezone = "Europe/Moscow"
)

// Service implements the Task Engine use cases.
//
// It is constructed with its collaborators (unit of work, clock, defaults) and
// holds no global state. Every method takes a context so cancellation and
// deadlines propagate to Infrastructure.
type Service struct {
	uow             UnitOfWork
	clock           Clock
	defaultTimezone string
}

// ServiceOption customises a Service.
type ServiceOption func(*Service)

// WithDefaultTimezone overrides the default IANA timezone for new tasks.
func WithDefaultTimezone(tz string) ServiceOption {
	return func(s *Service) {
		if tz != "" {
			s.defaultTimezone = tz
		}
	}
}

// NewService builds a Task Engine service.
func NewService(uow UnitOfWork, clock Clock, opts ...ServiceOption) *Service {
	if clock == nil {
		clock = SystemClock()
	}
	s := &Service{
		uow:             uow,
		clock:           clock,
		defaultTimezone: DefaultTimezone,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateTask creates a task and its `created` event in one transaction.
//
// Defaults are applied before the domain is called: an omitted priority becomes
// DefaultPriority and an omitted timezone becomes the configured default. The
// task and its event share the single timestamp returned by the clock.
func (s *Service) CreateTask(ctx context.Context, in CreateTaskInput, actor task.Actor) (TaskWithVersion, error) {
	if err := validateActor(actor); err != nil {
		return TaskWithVersion{}, err
	}

	now := s.clock.Now()

	priority := DefaultPriority
	if value, ok := in.Priority.Get(); ok {
		priority = value
	}

	timezone := s.defaultTimezone
	if value, ok := in.Timezone.Get(); ok {
		timezone = value
	}

	created, err := task.NewTask(in.Title, priority, timezone, now)
	if err != nil {
		return TaskWithVersion{}, err
	}

	opts := make([]task.TaskOption, 0, 6)
	if value, ok := in.Note.Get(); ok {
		opts = append(opts, task.WithNote(value))
	}
	if value, ok := in.PlannedDate.Get(); ok {
		opts = append(opts, task.WithPlannedDate(value))
	}
	if value, ok := in.DueDate.Get(); ok {
		opts = append(opts, task.WithDueDate(value))
	}
	if value, ok := in.DueAt.Get(); ok {
		opts = append(opts, task.WithDueAt(value))
	}
	if len(in.Tags) > 0 {
		opts = append(opts, task.WithTags(in.Tags))
	}
	if len(opts) > 0 {
		if err := created.Update(now, opts...); err != nil {
			return TaskWithVersion{}, err
		}
	}

	var result TaskWithVersion

	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		id, err := tx.Tasks().Insert(ctx, created)
		if err != nil {
			return err
		}
		created.ID = id

		payload := map[string]any{
			"source":           string(actor),
			"initial_priority": string(created.Priority),
		}
		event, err := created.ToEvent(task.EventTypeCreated, payload, actor, now)
		if err != nil {
			return err
		}
		if err := tx.Events().Insert(ctx, event); err != nil {
			return err
		}

		result = TaskWithVersion{Task: created, Version: 1}
		return nil
	})
	if err != nil {
		return TaskWithVersion{}, err
	}

	return result, nil
}

// GetTask returns a task and its version. Reads never bump the version.
func (s *Service) GetTask(ctx context.Context, id int64) (TaskWithVersion, error) {
	var result TaskWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Tasks().FindByID(ctx, id)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return TaskWithVersion{}, err
	}

	return result, nil
}

// ListTasks returns a filtered, sorted page of tasks.
//
// When the caller filters by overdue, the reference instant is taken from the
// injected clock and travels with the filter, so the repository evaluates
// overdue with the same deterministic semantics as the domain instead of
// reading a clock of its own.
func (s *Service) ListTasks(ctx context.Context, filter TaskFilter) (ListTasksResult, error) {
	normalized, err := normalizeFilter(filter)
	if err != nil {
		return ListTasksResult{}, err
	}

	if normalized.Overdue.IsSet() {
		normalized.AsOf = Some(s.clock.Now())
	}

	var result ListTasksResult

	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Tasks().List(ctx, normalized)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return ListTasksResult{}, err
	}

	return result, nil
}

// UpdateTask applies a partial update under optimistic locking.
//
// Only supplied fields change. The task is re-read inside the transaction, a
// deep snapshot is taken before any mutation, the options are applied through
// the domain (which validates and stays atomic), and the write is conditional on
// the version the caller supplied. The event payload is derived from the
// snapshot and the final state, never from the request.
func (s *Service) UpdateTask(ctx context.Context, id int64, expectedVersion int, in UpdateTaskInput, actor task.Actor) (TaskWithVersion, error) {
	if err := validateActor(actor); err != nil {
		return TaskWithVersion{}, err
	}

	now := s.clock.Now()
	var result TaskWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, id)
		if err != nil {
			return err
		}

		// Snapshot before mutating: Task.Update writes through the receiver, so
		// holding the same pointer would compare the task with itself.
		previous := cloneTask(current.Task)

		if opts := buildUpdateOptions(in); len(opts) > 0 {
			if err := current.Task.Update(now, opts...); err != nil {
				return err
			}
		}

		applyClears(current.Task, updateClears(in))

		updated, err := tx.Tasks().Update(ctx, id, expectedVersion, current.Task)
		if err != nil {
			return err
		}

		event, err := current.Task.ToEvent(task.EventTypeUpdated, changesPayload(diffTasks(previous, current.Task)), actor, now)
		if err != nil {
			return err
		}
		if err := tx.Events().Insert(ctx, event); err != nil {
			return err
		}

		result = updated
		return nil
	})
	if err != nil {
		return TaskWithVersion{}, err
	}

	return result, nil
}

// CompleteTask marks a task completed under optimistic locking.
func (s *Service) CompleteTask(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (TaskWithVersion, error) {
	return s.transition(ctx, id, expectedVersion, actor, task.EventTypeCompleted, (*task.Task).Complete, "completed")
}

// CancelTask marks a task cancelled under optimistic locking.
func (s *Service) CancelTask(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (TaskWithVersion, error) {
	return s.transition(ctx, id, expectedVersion, actor, task.EventTypeCancelled, (*task.Task).Cancel, "cancelled")
}

// RestoreTask reopens a completed or cancelled task under optimistic locking.
func (s *Service) RestoreTask(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (TaskWithVersion, error) {
	return s.transition(ctx, id, expectedVersion, actor, task.EventTypeRestored, (*task.Task).Restore, "restored")
}

// RescheduleTask changes scheduling fields under optimistic locking.
//
// Clearing works exactly as in UpdateTask: a zero CalendarDate or the zero
// time.Time means "remove the deadline" and is never passed to the domain
// options. A reschedule whose only change is a clear still goes through the
// domain (so UpdatedAt moves), still bumps the version by one, and still writes
// its event. The domain refuses to reschedule a task that is not open; that
// error is returned unchanged.
func (s *Service) RescheduleTask(ctx context.Context, id int64, expectedVersion int, in RescheduleTaskInput, actor task.Actor) (TaskWithVersion, error) {
	if err := validateActor(actor); err != nil {
		return TaskWithVersion{}, err
	}

	now := s.clock.Now()
	var result TaskWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, id)
		if err != nil {
			return err
		}

		if !in.anySet() {
			result = current
			return nil
		}

		previous := cloneTask(current.Task)

		if opts := buildRescheduleOptions(in); len(opts) > 0 {
			if err := current.Task.Reschedule(now, opts...); err != nil {
				return err
			}
		} else {
			// Only clears were requested. Reschedule with no options still
			// validates the open status and moves UpdatedAt, so the operation is
			// not a silent no-op.
			if err := current.Task.Reschedule(now); err != nil {
				return err
			}
		}

		applyClears(current.Task, rescheduleClears(in))

		updated, err := tx.Tasks().Update(ctx, id, expectedVersion, current.Task)
		if err != nil {
			return err
		}

		event, err := current.Task.ToEvent(task.EventTypeRescheduled, changesPayload(diffScheduling(current.Task, previous)), actor, now)
		if err != nil {
			return err
		}
		if err := tx.Events().Insert(ctx, event); err != nil {
			return err
		}

		result = updated
		return nil
	})
	if err != nil {
		return TaskWithVersion{}, err
	}

	return result, nil
}

// transition runs a status change and writes its event atomically.
func (s *Service) transition(
	ctx context.Context,
	id int64,
	expectedVersion int,
	actor task.Actor,
	eventType task.EventType,
	apply func(*task.Task, time.Time) error,
	field string,
) (TaskWithVersion, error) {
	if err := validateActor(actor); err != nil {
		return TaskWithVersion{}, err
	}

	now := s.clock.Now()
	var result TaskWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, id)
		if err != nil {
			return err
		}

		if err := apply(current.Task, now); err != nil {
			return err
		}

		updated, err := tx.Tasks().Update(ctx, id, expectedVersion, current.Task)
		if err != nil {
			return err
		}

		event, err := current.Task.ToEvent(eventType, map[string]any{"status": field}, actor, now)
		if err != nil {
			return err
		}
		if err := tx.Events().Insert(ctx, event); err != nil {
			return err
		}

		result = updated
		return nil
	})
	if err != nil {
		return TaskWithVersion{}, err
	}

	return result, nil
}

// buildUpdateOptions converts a partial update into domain options.
//
// A zero DueAt is a clear request, not a value: WithDueAt rejects the zero time,
// so it is deliberately omitted here and handled by applyClears.
func buildUpdateOptions(in UpdateTaskInput) []task.TaskOption {
	opts := make([]task.TaskOption, 0, 8)

	if value, ok := in.Title.Get(); ok {
		opts = append(opts, task.WithTitle(value))
	}
	if value, ok := in.Note.Get(); ok {
		opts = append(opts, task.WithNote(value))
	}
	if value, ok := in.Priority.Get(); ok {
		opts = append(opts, task.WithPriority(value))
	}
	if value, ok := in.PlannedDate.Get(); ok {
		opts = append(opts, task.WithPlannedDate(value))
	}
	if value, ok := in.DueDate.Get(); ok {
		opts = append(opts, task.WithDueDate(value))
	}
	if value, ok := in.DueAt.Get(); ok && !value.IsZero() {
		opts = append(opts, task.WithDueAt(value))
	}
	if value, ok := in.Timezone.Get(); ok {
		opts = append(opts, task.WithTimezone(value))
	}
	if value, ok := in.Tags.Get(); ok {
		opts = append(opts, task.WithTags(value))
	}

	return opts
}

// buildRescheduleOptions converts reschedule input into domain options,
// skipping a zero DueAt for the same reason as buildUpdateOptions.
func buildRescheduleOptions(in RescheduleTaskInput) []task.TaskOption {
	opts := make([]task.TaskOption, 0, 3)

	if value, ok := in.PlannedDate.Get(); ok && !value.IsZero() {
		opts = append(opts, task.WithPlannedDate(value))
	}
	if value, ok := in.DueDate.Get(); ok && !value.IsZero() {
		opts = append(opts, task.WithDueDate(value))
	}
	if value, ok := in.DueAt.Get(); ok && !value.IsZero() {
		opts = append(opts, task.WithDueAt(value))
	}

	return opts
}

// nullableClear records which nullable fields a use case was asked to clear.
//
// It is the single place where the "explicit zero means absent" rule is
// interpreted, so UpdateTask and RescheduleTask cannot drift apart.
type nullableClear struct {
	note        bool
	plannedDate bool
	dueDate     bool
	dueAt       bool
	tags        bool
}

func (c nullableClear) any() bool {
	return c.note || c.plannedDate || c.dueDate || c.dueAt || c.tags
}

// updateClears derives the clear set from an UpdateTask request.
func updateClears(in UpdateTaskInput) nullableClear {
	var c nullableClear
	if value, ok := in.Note.Get(); ok && value == "" {
		c.note = true
	}
	if value, ok := in.PlannedDate.Get(); ok && value.IsZero() {
		c.plannedDate = true
	}
	if value, ok := in.DueDate.Get(); ok && value.IsZero() {
		c.dueDate = true
	}
	if value, ok := in.DueAt.Get(); ok && value.IsZero() {
		c.dueAt = true
	}
	if value, ok := in.Tags.Get(); ok && len(value) == 0 {
		c.tags = true
	}
	return c
}

// rescheduleClears derives the clear set from a RescheduleTask request.
func rescheduleClears(in RescheduleTaskInput) nullableClear {
	var c nullableClear
	if value, ok := in.PlannedDate.Get(); ok && value.IsZero() {
		c.plannedDate = true
	}
	if value, ok := in.DueDate.Get(); ok && value.IsZero() {
		c.dueDate = true
	}
	if value, ok := in.DueAt.Get(); ok && value.IsZero() {
		c.dueAt = true
	}
	return c
}

// applyClears stores "absent" for the fields the caller explicitly cleared.
//
// The domain options have already validated any real value; here the aggregate
// is only normalised to nil/empty. No validation is duplicated — Validate
// already treats a nil pointer and an empty slice as absent.
func applyClears(t *task.Task, c nullableClear) {
	if c.note {
		t.Note = nil
	}
	if c.plannedDate {
		t.PlannedDate = nil
	}
	if c.dueDate {
		t.DueDate = nil
	}
	if c.dueAt {
		t.DueAt = nil
	}
	if c.tags {
		t.Tags = []string{}
	}
}

// fieldChange is one normalised before/after pair in an audit payload.
type fieldChange struct {
	Field string `json:"field"`
	Old   any    `json:"old"`
	New   any    `json:"new"`
}

// changesPayload renders the audit payload for a set of changed fields.
//
// One changed field keeps the documented single-field shape
// ({field, old, new}); several use {changes: [...]}. Both come from the same
// structure, so a single-field update is just a one-element list.
func changesPayload(changes []fieldChange) map[string]any {
	if len(changes) == 0 {
		return map[string]any{"changes": []fieldChange{}}
	}
	if len(changes) == 1 {
		return map[string]any{
			"field": changes[0].Field,
			"old":   changes[0].Old,
			"new":   changes[0].New,
		}
	}
	return map[string]any{"changes": changes}
}

// diffTasks returns the changed fields between two task states, in a stable
// field order so payloads are reproducible.
func diffTasks(before, after *task.Task) []fieldChange {
	changes := make([]fieldChange, 0, 8)

	if before.Title != after.Title {
		changes = append(changes, fieldChange{Field: "title", Old: before.Title, New: after.Title})
	}
	if !equalStringPtr(before.Note, after.Note) {
		changes = append(changes, fieldChange{Field: "note", Old: normalizeStringPtr(before.Note), New: normalizeStringPtr(after.Note)})
	}
	if before.Priority != after.Priority {
		changes = append(changes, fieldChange{Field: "priority", Old: string(before.Priority), New: string(after.Priority)})
	}
	if !equalCalendarPtr(before.PlannedDate, after.PlannedDate) {
		changes = append(changes, fieldChange{Field: "planned_date", Old: normalizeCalendarDate(before.PlannedDate), New: normalizeCalendarDate(after.PlannedDate)})
	}
	if !equalCalendarPtr(before.DueDate, after.DueDate) {
		changes = append(changes, fieldChange{Field: "due_date", Old: normalizeCalendarDate(before.DueDate), New: normalizeCalendarDate(after.DueDate)})
	}
	if !equalTimePtr(before.DueAt, after.DueAt) {
		changes = append(changes, fieldChange{Field: "due_at", Old: normalizeTime(before.DueAt), New: normalizeTime(after.DueAt)})
	}
	if before.Timezone != after.Timezone {
		changes = append(changes, fieldChange{Field: "timezone", Old: before.Timezone, New: after.Timezone})
	}
	if !equalStrings(before.Tags, after.Tags) {
		changes = append(changes, fieldChange{Field: "tags", Old: normalizeTags(before.Tags), New: normalizeTags(after.Tags)})
	}

	return changes
}

// diffScheduling is diffTasks restricted to the scheduling fields, which is all
// a reschedule may change. The order matches diffTasks so a payload is identical
// whether it came from an update or a reschedule.
func diffScheduling(after, before *task.Task) []fieldChange {
	all := diffTasks(before, after)
	changes := make([]fieldChange, 0, 3)
	for _, change := range all {
		switch change.Field {
		case "planned_date", "due_date", "due_at":
			changes = append(changes, change)
		}
	}
	return changes
}

// Normalised audit values. Domain pointers and structs must never reach the
// payload: everything is rendered as a JSON scalar, string or array.

func normalizeStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func normalizeCalendarDate(value *task.CalendarDate) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.String()
}

func normalizeTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func normalizeTags(value []string) any {
	if value == nil {
		return nil
	}
	tags := make([]string, len(value))
	copy(tags, value)
	return tags
}

// normalizeFilter applies defaults and validates list parameters.
func normalizeFilter(filter TaskFilter) (TaskFilter, error) {
	if filter.Limit.IsSet() {
		limit := filter.Limit.Or(DefaultPageLimit)
		if limit < 1 || limit > MaxPageLimit {
			return TaskFilter{}, fmt.Errorf("%w: got %d", ErrInvalidLimit, limit)
		}
		filter.Limit = Some(limit)
	} else {
		filter.Limit = Some(DefaultPageLimit)
	}

	if filter.Offset.IsSet() {
		offset := filter.Offset.Or(0)
		if offset < 0 {
			return TaskFilter{}, fmt.Errorf("%w: got %d", ErrInvalidOffset, offset)
		}
		filter.Offset = Some(offset)
	} else {
		filter.Offset = Some(0)
	}

	for _, sort := range filter.Sorts {
		switch sort.Field {
		case SortCreatedAt, SortUpdatedAt, SortDueAt, SortPriority, SortTitle:
		default:
			return TaskFilter{}, fmt.Errorf("%w: %q", ErrInvalidSort, sort.Field)
		}
		if sort.Order != SortAsc && sort.Order != SortDesc {
			return TaskFilter{}, fmt.Errorf("%w: %q", ErrInvalidSort, sort.Order)
		}
	}

	return filter, nil
}

// validateActor rejects an actor the domain would refuse to store.
func validateActor(actor task.Actor) error {
	if !actor.IsValid() {
		return task.ErrInvalidActor
	}
	return nil
}

// cloneTask returns a deep copy of a task with no shared mutable state.
//
// The application needs this to snapshot an aggregate before mutating it; the
// domain deliberately exposes no Clone, so the copy lives here where the need
// for it arises.
func cloneTask(t *task.Task) *task.Task {
	if t == nil {
		return nil
	}
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
		value := *t.CompletedAt
		copied.CompletedAt = &value
	}
	if t.CancelledAt != nil {
		value := *t.CancelledAt
		copied.CancelledAt = &value
	}
	if t.PlannedDate != nil {
		value := *t.PlannedDate
		copied.PlannedDate = &value
	}
	if t.DueDate != nil {
		value := *t.DueDate
		copied.DueDate = &value
	}
	if t.Tags != nil {
		tags := make([]string, len(t.Tags))
		copy(tags, t.Tags)
		copied.Tags = tags
	}
	return &copied
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalCalendarPtr(a, b *task.CalendarDate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
