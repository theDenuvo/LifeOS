package events

import (
	"context"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/event"
)

// Repository persists calendar events.
type Repository interface {
	Insert(ctx context.Context, value *event.Event) (int64, error)
	FindByID(ctx context.Context, id int64) (EventWithVersion, error)
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]EventWithVersion, error)
	Update(ctx context.Context, id int64, expectedVersion int, value *event.Event) (EventWithVersion, error)
	Delete(ctx context.Context, id int64, expectedVersion int) error
}

// Transaction exposes the event repository.
type Transaction interface {
	Events() Repository
}

// UnitOfWork defines the transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}
