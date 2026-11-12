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
func (s *Service) ListTasks(ctx context.Context, filter TaskFilter) (ListTasksResult, error) {
	normalized, err := normalizeFilter(filter)
	if err != nil {
		return ListTasksResult{}, err
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
// Only supplied fields change. The task is re-read inside the transaction, the
// options are applied through the domain (which validates and stays atomic),
// and the write is conditional on the version the caller supplied.
func (s *Service) UpdateTask(ctx context.Context, id int64, expectedVersion int, in UpdateTaskInput, actor task.Actor) (TaskWithVersion, error) {
	if err := validateActor(actor); err != nil {
		return TaskWithVersion{}, err
	}

	now := s.clock.Now()
	var result TaskWithVersion
	var previous *task.Task

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, id)
		if err != nil {
			return err
		}
		previous = current.Task

		opts := buildUpdateOptions(in)
		if len(opts) > 0 {
			if err := current.Task.Update(now, opts...); err != nil {
				return err
			}
		}

		// Clearing a nullable field means storing "absent", which the domain
		// already treats as a valid state (Validate only inspects non-nil
		// values). The domain has no "clear" option, and the aggregate is the
		// only place that can express absence, so the nil assignment happens
		// here rather than by duplicating any validation.
		applyClearFields(current.Task, in)

		updated, err := tx.Tasks().Update(ctx, id, expectedVersion, current.Task)
		if err != nil {
			return err
		}

		event, err := current.Task.ToEvent(task.EventTypeUpdated, updatedPayload(previous, current.Task), actor, now)
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
// The domain refuses to reschedule a task that is not open; that error is
// returned as-is so callers can match it with task.IsInvalidTransitionError.
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

		opts := make([]task.TaskOption, 0, 3)
		if value, ok := in.PlannedDate.Get(); ok {
			opts = append(opts, task.WithPlannedDate(value))
		}
		if value, ok := in.DueDate.Get(); ok {
			opts = append(opts, task.WithDueDate(value))
		}
		if value, ok := in.DueAt.Get(); ok {
			opts = append(opts, task.WithDueAt(value))
		}
		if len(opts) == 0 {
			result = current
			return nil
		}

		if err := current.Task.Reschedule(now, opts...); err != nil {
			return err
		}

		updated, err := tx.Tasks().Update(ctx, id, expectedVersion, current.Task)
		if err != nil {
			return err
		}

		payload := map[string]any{"fields": describeReschedule(in)}
		event, err := current.Task.ToEvent(task.EventTypeRescheduled, payload, actor, now)
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
	if value, ok := in.DueAt.Get(); ok {
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

// applyClearFields removes nullable fields the caller explicitly cleared.
//
// A zero CalendarDate or an empty note means "no value" in the contract; the
// domain options would store a pointer to that zero value instead of removing
// it, so the aggregate is normalised here. No validation is duplicated: the
// values have already passed the domain's option checks, and Validate treats a
// nil pointer as absent.
func applyClearFields(t *task.Task, in UpdateTaskInput) {
	if value, ok := in.Note.Get(); ok && value == "" {
		t.Note = nil
	}
	if value, ok := in.PlannedDate.Get(); ok && value.IsZero() {
		t.PlannedDate = nil
	}
	if value, ok := in.DueDate.Get(); ok && value.IsZero() {
		t.DueDate = nil
	}
	if value, ok := in.Tags.Get(); ok && len(value) == 0 {
		t.Tags = []string{}
	}
}

// updatedPayload reports which fields a partial update changed.
//
// DATA_MODEL.md specifies a single-field shape for `updated` events; a partial
// update can touch several fields, so the payload lists every changed field
// under "fields" and keeps the documented keys for the single-field case.
func updatedPayload(before, after *task.Task) map[string]any {
	fields := make([]string, 0, 8)

	if before.Title != after.Title {
		fields = append(fields, "title")
	}
	if !equalStringPtr(before.Note, after.Note) {
		fields = append(fields, "note")
	}
	if before.Priority != after.Priority {
		fields = append(fields, "priority")
	}
	if !equalCalendarPtr(before.PlannedDate, after.PlannedDate) {
		fields = append(fields, "planned_date")
	}
	if !equalCalendarPtr(before.DueDate, after.DueDate) {
		fields = append(fields, "due_date")
	}
	if !equalTimePtr(before.DueAt, after.DueAt) {
		fields = append(fields, "due_at")
	}
	if before.Timezone != after.Timezone {
		fields = append(fields, "timezone")
	}
	if !equalStrings(before.Tags, after.Tags) {
		fields = append(fields, "tags")
	}

	payload := map[string]any{"fields": fields}
	if len(fields) == 1 {
		payload["field"] = fields[0]
	}
	return payload
}

// describeReschedule summarises which scheduling fields were requested.
func describeReschedule(in RescheduleTaskInput) []string {
	fields := make([]string, 0, 3)
	if in.PlannedDate.IsSet() {
		fields = append(fields, "planned_date")
	}
	if in.DueDate.IsSet() {
		fields = append(fields, "due_date")
	}
	if in.DueAt.IsSet() {
		fields = append(fields, "due_at")
	}
	return fields
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
