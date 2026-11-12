// Package inbox implements the quick-capture use cases: list, capture,
// convert to a task and dismiss.
//
// Conversion is the interesting one: it creates a task, writes its audit event
// and marks the inbox item processed inside a single unit of work, so a failure
// at any step leaves the inbox untouched.
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domaininbox "github.com/theDenuvo/LifeOS/internal/domain/inbox"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// Application errors.
var (
	// ErrItemNotFound is returned when no inbox item exists with the requested ID.
	ErrItemNotFound = errors.New("inbox item not found")

	// ErrAlreadyProcessed is returned when an item was already converted or dismissed.
	ErrAlreadyProcessed = errors.New("inbox item already processed")

	// ErrInvalidLimit is returned when a page size falls outside 1..MaxPageLimit.
	ErrInvalidLimit = errors.New("limit must be between 1 and 100")

	// ErrInvalidOffset is returned when a negative offset is supplied.
	ErrInvalidOffset = errors.New("offset must not be negative")
)

// Page limits mirror the task list contract.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 100
)

// Repository persists inbox items. It is a port implemented by Infrastructure.
type Repository interface {
	FindByID(ctx context.Context, id int64) (*domaininbox.Item, error)
	List(ctx context.Context, filter Filter) (ListResult, error)
	Insert(ctx context.Context, item *domaininbox.Item) (int64, error)
	MarkProcessed(ctx context.Context, id int64, processedAt time.Time, taskID int64) error
	Delete(ctx context.Context, id int64) error
	CountUnprocessed(ctx context.Context) (int, error)
	Search(ctx context.Context, query string, limit int) ([]*domaininbox.Item, int, error)
}

// Transaction exposes the repositories bound to one transaction.
//
// Conversion needs the task and event repositories too, so the inbox
// transaction embeds the task transaction instead of duplicating it.
type Transaction interface {
	tasks.Transaction
	Inbox() Repository
}

// UnitOfWork defines the transaction boundary for inbox use cases.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Filter is the typed inbox list filter.
type Filter struct {
	UnprocessedOnly bool
	Limit           tasks.Optional[int]
	Offset          tasks.Optional[int]
}

// ListResult is a page of inbox items plus the total count.
type ListResult struct {
	Items  []*domaininbox.Item
	Total  int
	Limit  int
	Offset int
}

// ConvertInput carries optional task fields for a conversion.
type ConvertInput struct {
	Priority    tasks.Optional[task.Priority]
	PlannedDate tasks.Optional[task.CalendarDate]
	DueDate     tasks.Optional[task.CalendarDate]
	DueAt       tasks.Optional[time.Time]
	Timezone    tasks.Optional[string]
}

// Service implements the inbox use cases.
type Service struct {
	uow             UnitOfWork
	clock           tasks.Clock
	defaultTimezone string
	publisher       events.Publisher
}

// Option customises an inbox service.
type Option func(*Service)

// WithPublisher attaches an event publisher. Mutations publish after their
// transaction commits.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) {
		s.publisher = publisher
	}
}

// NewService builds an inbox service.
func NewService(uow UnitOfWork, clock tasks.Clock, defaultTimezone string, opts ...Option) *Service {
	if clock == nil {
		clock = tasks.SystemClock()
	}
	if defaultTimezone == "" {
		defaultTimezone = tasks.DefaultTimezone
	}
	s := &Service{uow: uow, clock: clock, defaultTimezone: defaultTimezone}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Capture stores a new inbox item.
func (s *Service) Capture(ctx context.Context, text string, meta json.RawMessage) (*domaininbox.Item, error) {
	now := s.clock.Now()
	item, err := domaininbox.NewItem(text, meta, now)
	if err != nil {
		return nil, err
	}

	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		id, err := tx.Inbox().Insert(ctx, item)
		if err != nil {
			return err
		}
		item.ID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("inbox.captured", map[string]any{"id": item.ID})
	return item, nil
}

// List returns a page of inbox items.
func (s *Service) List(ctx context.Context, filter Filter) (ListResult, error) {
	limit := DefaultPageLimit
	if value, ok := filter.Limit.Get(); ok {
		limit = value
	}
	if limit < 1 || limit > MaxPageLimit {
		return ListResult{}, fmt.Errorf("%w: got %d", ErrInvalidLimit, limit)
	}

	offset := 0
	if value, ok := filter.Offset.Get(); ok {
		offset = value
	}
	if offset < 0 {
		return ListResult{}, fmt.Errorf("%w: got %d", ErrInvalidOffset, offset)
	}

	filter.Limit = tasks.Some(limit)
	filter.Offset = tasks.Some(offset)

	var result ListResult
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Inbox().List(ctx, filter)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}

// CountUnprocessed returns the number of items still waiting for triage.
func (s *Service) CountUnprocessed(ctx context.Context) (int, error) {
	var count int
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		value, err := tx.Inbox().CountUnprocessed(ctx)
		if err != nil {
			return err
		}
		count = value
		return nil
	})
	return count, err
}

// Search looks for captured text.
func (s *Service) Search(ctx context.Context, query string, limit int) (ListResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return ListResult{}, fmt.Errorf("%w: query is required", ErrInvalidLimit)
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}

	var result ListResult
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		items, total, err := tx.Inbox().Search(ctx, query, limit)
		if err != nil {
			return err
		}
		result = ListResult{Items: items, Total: total, Limit: limit, Offset: 0}
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}

// ConvertToTask turns an inbox item into a task and marks the item processed.
//
// The task title is the captured text, truncated to the task title limit; when
// truncation happens the full text is preserved as the task note so nothing the
// user captured is lost.
func (s *Service) ConvertToTask(ctx context.Context, id int64, in ConvertInput) (tasks.TaskWithVersion, error) {
	now := s.clock.Now()
	var result tasks.TaskWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		item, err := tx.Inbox().FindByID(ctx, id)
		if err != nil {
			return err
		}
		if item.IsProcessed() {
			return ErrAlreadyProcessed
		}

		priority := tasks.DefaultPriority
		if value, ok := in.Priority.Get(); ok {
			priority = value
		}
		timezone := s.defaultTimezone
		if value, ok := in.Timezone.Get(); ok {
			timezone = value
		}

		title, note := splitCapturedText(item.Text)
		created, err := task.NewTask(title, priority, timezone, now)
		if err != nil {
			return err
		}

		opts := make([]task.TaskOption, 0, 4)
		if note != "" {
			opts = append(opts, task.WithNote(note))
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
		if len(opts) > 0 {
			if err := created.Update(now, opts...); err != nil {
				return err
			}
		}

		taskID, err := tx.Tasks().Insert(ctx, created)
		if err != nil {
			return err
		}
		created.ID = taskID

		event, err := created.ToEvent(task.EventTypeConvertedFromInbox, map[string]any{
			"inbox_item_id": item.ID,
			"inbox_text":    item.Text,
		}, task.ActorUser, now)
		if err != nil {
			return err
		}
		if err := tx.Events().Insert(ctx, event); err != nil {
			return err
		}
		if err := tx.Inbox().MarkProcessed(ctx, item.ID, now, taskID); err != nil {
			return err
		}

		result = tasks.TaskWithVersion{Task: created, Version: 1}
		return nil
	})
	if err != nil {
		return tasks.TaskWithVersion{}, err
	}
	s.publish("inbox.converted", map[string]any{
		"inbox_id": id,
		"task_id":  result.Task.ID,
		"version":  result.Version,
	})
	return result, nil
}

// Dismiss deletes an unprocessed inbox item.
func (s *Service) Dismiss(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		item, err := tx.Inbox().FindByID(ctx, id)
		if err != nil {
			return err
		}
		if item.IsProcessed() {
			return ErrAlreadyProcessed
		}
		return tx.Inbox().Delete(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("inbox.dismissed", map[string]any{"id": id})
	return nil
}

// publish emits a change notification when a publisher is configured.
func (s *Service) publish(eventType string, data any) {
	if s.publisher == nil {
		return
	}
	s.publisher.Publish(eventType, data)
}

// splitCapturedText fits captured text into the task title limit.
func splitCapturedText(text string) (string, string) {
	const maxTitleRunes = 500
	if utf8.RuneCountInString(text) <= maxTitleRunes {
		return text, ""
	}
	runes := []rune(text)
	return string(runes[:maxTitleRunes]), text
}
