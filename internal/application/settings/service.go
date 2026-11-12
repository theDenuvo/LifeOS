// Package settings stores user preferences as JSON values.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// Errors.
var (
	ErrNotFound     = errors.New("setting not found")
	ErrInvalidKey   = errors.New("setting key must match [a-z0-9_.-]{1,64}")
	ErrInvalidValue = errors.New("setting value must be valid JSON under 64 KiB")
)

var keyPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

const maxValueBytes = 64 << 10

// Repository persists settings.
type Repository interface {
	Get(ctx context.Context, key string) (json.RawMessage, bool, error)
	List(ctx context.Context) (map[string]json.RawMessage, error)
	Put(ctx context.Context, key string, value json.RawMessage, at time.Time) error
}

// Transaction exposes the settings repository.
type Transaction interface {
	Settings() Repository
}

// UnitOfWork defines the transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Service reads and writes settings.
type Service struct {
	uow   UnitOfWork
	clock Clock
}

// NewService builds the settings service.
func NewService(uow UnitOfWork, clock Clock) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{uow: uow, clock: clock}
}

// Get returns one setting value.
func (s *Service) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	var value json.RawMessage
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, ok, err := tx.Settings().Get(ctx, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		value = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

// Put stores a setting value.
func (s *Service) Put(ctx context.Context, key string, value json.RawMessage) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	if len(value) == 0 || len(value) > maxValueBytes || !json.Valid(value) {
		return ErrInvalidValue
	}
	now := s.clock.Now()
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Settings().Put(ctx, key, value, now)
	})
}

// List returns all settings.
func (s *Service) List(ctx context.Context) (map[string]json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Settings().List(ctx)
		if err != nil {
			return err
		}
		values = found
		return nil
	})
	return values, err
}
