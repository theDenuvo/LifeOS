package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/event"
	eventbus "github.com/theDenuvo/LifeOS/internal/events"
)

// DefaultTimezone is used when neither the input nor the service has one.
const DefaultTimezone = "Europe/Moscow"

// Service implements calendar-event use cases.
type Service struct {
	uow             UnitOfWork
	clock           Clock
	defaultTimezone string
	publisher       eventbus.Publisher
}

// Option customises the service.
type Option func(*Service)

// WithTimezone sets the fallback timezone for new events.
func WithTimezone(timezone string) Option {
	return func(s *Service) {
		if timezone != "" {
			s.defaultTimezone = timezone
		}
	}
}

// WithPublisher attaches the in-process event bus.
func WithPublisher(publisher eventbus.Publisher) Option {
	return func(s *Service) {
		s.publisher = publisher
	}
}

// NewService builds the event service.
func NewService(uow UnitOfWork, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
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

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Create stores a new event.
func (s *Service) Create(ctx context.Context, in CreateEventInput) (EventWithVersion, error) {
	now := s.clock.Now()

	timezone := s.defaultTimezone
	if in.Timezone != nil && *in.Timezone != "" {
		timezone = *in.Timezone
	}

	calendar := event.CalendarPersonal
	if in.Calendar != "" {
		calendar = event.Calendar(in.Calendar)
	}

	endsAt := in.StartsAt.Add(time.Hour)
	if in.AllDay {
		endsAt = in.StartsAt.AddDate(0, 0, 1)
	}
	if in.EndsAt != nil {
		endsAt = *in.EndsAt
	}

	created, err := event.NewEvent(
		in.Title,
		in.Description,
		in.Location,
		calendar,
		in.StartsAt,
		endsAt,
		in.AllDay,
		timezone,
		now,
	)
	if err != nil {
		return EventWithVersion{}, err
	}

	var result EventWithVersion
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		id, err := tx.Events().Insert(ctx, created)
		if err != nil {
			return err
		}
		created.ID = id
		result = EventWithVersion{Event: created, Version: 1}
		return nil
	})
	if err != nil {
		return EventWithVersion{}, err
	}

	s.publish("event.created", result)
	return result, nil
}

// Get returns one event.
func (s *Service) Get(ctx context.Context, id int64) (EventWithVersion, error) {
	var result EventWithVersion
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Events().FindByID(ctx, id)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return EventWithVersion{}, err
	}
	return result, nil
}

// ListRange returns events overlapping [from, to).
func (s *Service) ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]EventWithVersion, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, ErrInvalidDateRange
	}
	for _, calendar := range calendars {
		if !calendar.IsValid() {
			return nil, fmt.Errorf("%w: %s", ErrInvalidCalendar, calendar)
		}
	}

	var result []EventWithVersion
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Events().ListRange(ctx, from, to, calendars)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Update applies a partial update under optimistic locking.
func (s *Service) Update(ctx context.Context, id int64, expectedVersion int, in UpdateEventInput) (EventWithVersion, error) {
	now := s.clock.Now()
	var result EventWithVersion

	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Events().FindByID(ctx, id)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return ErrVersionConflict
		}

		value := current.Event
		if in.Title != nil {
			value.Title = *in.Title
		}
		if in.Description != nil {
			if *in.Description == "" {
				value.Description = nil
			} else {
				description := *in.Description
				value.Description = &description
			}
		}
		if in.Location != nil {
			if *in.Location == "" {
				value.Location = nil
			} else {
				location := *in.Location
				value.Location = &location
			}
		}
		if in.Calendar != nil {
			value.Calendar = event.Calendar(*in.Calendar)
		}
		if in.StartsAt != nil {
			value.StartsAt = in.StartsAt.UTC()
		}
		if in.EndsAt != nil {
			value.EndsAt = in.EndsAt.UTC()
		}
		if in.AllDay != nil {
			value.AllDay = *in.AllDay
		}
		if in.Timezone != nil && *in.Timezone != "" {
			value.Timezone = *in.Timezone
		}
		value.UpdatedAt = now.UTC()
		if err := value.Validate(); err != nil {
			return err
		}

		updated, err := tx.Events().Update(ctx, id, expectedVersion, value)
		if err != nil {
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return EventWithVersion{}, err
	}

	s.publish("event.updated", result)
	return result, nil
}

// Delete removes an event under optimistic locking.
func (s *Service) Delete(ctx context.Context, id int64, expectedVersion int) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Events().Delete(ctx, id, expectedVersion)
	})
	if err != nil {
		return err
	}
	s.publish("event.deleted", map[string]any{"id": id})
	return nil
}

func (s *Service) publish(eventType string, data any) {
	if s.publisher == nil {
		return
	}
	s.publisher.Publish(eventType, data)
}

// NotFound reports whether err means "no such event".
func NotFound(err error) bool { return errors.Is(err, ErrEventNotFound) }

// VersionConflict reports whether err means "stale version".
func VersionConflict(err error) bool { return errors.Is(err, ErrVersionConflict) }
