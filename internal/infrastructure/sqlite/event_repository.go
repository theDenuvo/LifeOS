package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
)

// EventUnitOfWork implements events.UnitOfWork on top of SQLite.
type EventUnitOfWork struct {
	db *sql.DB
}

// NewEventUnitOfWork builds an event unit of work.
func NewEventUnitOfWork(db *sql.DB) *EventUnitOfWork {
	return &EventUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *EventUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx appevents.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, eventTransaction{tx: tx})
	})
}

type eventTransaction struct {
	tx *sql.Tx
}

func (t eventTransaction) Events() appevents.Repository { return &eventRepository{tx: t.tx} }

type eventRepository struct {
	tx *sql.Tx
}

const eventColumns = `id, title, description, location, calendar, starts_at, ends_at,
	all_day, timezone, created_at, updated_at, version`

// Insert stores a new event.
func (r *eventRepository) Insert(ctx context.Context, value *event.Event) (int64, error) {
	if err := value.Validate(); err != nil {
		return 0, err
	}
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO calendar_events (
			title, description, location, calendar, starts_at, ends_at,
			all_day, timezone, created_at, updated_at, version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		value.Title,
		encodeStringPtr(value.Description),
		encodeStringPtr(value.Location),
		string(value.Calendar),
		unixSeconds(value.StartsAt),
		unixSeconds(value.EndsAt),
		boolInt(value.AllDay),
		value.Timezone,
		unixSeconds(value.CreatedAt),
		unixSeconds(value.UpdatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert event: read id: %w", err)
	}
	return id, nil
}

// FindByID loads one event.
func (r *eventRepository) FindByID(ctx context.Context, id int64) (appevents.EventWithVersion, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM calendar_events WHERE id = ?`, id)
	value, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return appevents.EventWithVersion{}, appevents.ErrEventNotFound
	}
	if err != nil {
		return appevents.EventWithVersion{}, fmt.Errorf("find event %d: %w", id, err)
	}
	return value, nil
}

// ListRange returns events overlapping [from, to).
func (r *eventRepository) ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]appevents.EventWithVersion, error) {
	query := `SELECT ` + eventColumns + ` FROM calendar_events WHERE starts_at < ? AND ends_at > ?`
	args := []any{unixSeconds(to), unixSeconds(from)}

	if len(calendars) > 0 {
		placeholders := make([]string, len(calendars))
		for i, calendar := range calendars {
			placeholders[i] = "?"
			args = append(args, string(calendar))
		}
		query += " AND calendar IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY starts_at ASC, id ASC"

	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	events := make([]appevents.EventWithVersion, 0, 16)
	for rows.Next() {
		value, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan events: %w", err)
	}
	return events, nil
}

// Update performs a conditional write and bumps the version.
func (r *eventRepository) Update(ctx context.Context, id int64, expectedVersion int, value *event.Event) (appevents.EventWithVersion, error) {
	if err := value.Validate(); err != nil {
		return appevents.EventWithVersion{}, err
	}
	result, err := r.tx.ExecContext(ctx, `
		UPDATE calendar_events SET
			title = ?, description = ?, location = ?, calendar = ?, starts_at = ?,
			ends_at = ?, all_day = ?, timezone = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		value.Title,
		encodeStringPtr(value.Description),
		encodeStringPtr(value.Location),
		string(value.Calendar),
		unixSeconds(value.StartsAt),
		unixSeconds(value.EndsAt),
		boolInt(value.AllDay),
		value.Timezone,
		unixSeconds(value.UpdatedAt),
		id,
		expectedVersion,
	)
	if err != nil {
		return appevents.EventWithVersion{}, fmt.Errorf("update event %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return appevents.EventWithVersion{}, fmt.Errorf("update event %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.FindByID(ctx, id); errors.Is(err, appevents.ErrEventNotFound) {
			return appevents.EventWithVersion{}, appevents.ErrEventNotFound
		}
		return appevents.EventWithVersion{}, appevents.ErrVersionConflict
	}
	return r.FindByID(ctx, id)
}

// Delete removes an event under optimistic locking.
func (r *eventRepository) Delete(ctx context.Context, id int64, expectedVersion int) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE id = ? AND version = ?`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete event %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete event %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		if _, err := r.FindByID(ctx, id); errors.Is(err, appevents.ErrEventNotFound) {
			return appevents.ErrEventNotFound
		}
		return appevents.ErrVersionConflict
	}
	return nil
}

func scanEvent(row rowScanner) (appevents.EventWithVersion, error) {
	var (
		id          int64
		title       string
		description sql.NullString
		location    sql.NullString
		calendar    string
		startsAt    int64
		endsAt      int64
		allDay      int64
		timezone    string
		createdAt   int64
		updatedAt   int64
		version     int
	)
	if err := row.Scan(
		&id, &title, &description, &location, &calendar, &startsAt, &endsAt,
		&allDay, &timezone, &createdAt, &updatedAt, &version,
	); err != nil {
		return appevents.EventWithVersion{}, err
	}
	value := &event.Event{
		ID:          id,
		Title:       title,
		Description: stringPtr(description),
		Location:    stringPtr(location),
		Calendar:    event.Calendar(calendar),
		StartsAt:    timeFromUnix(startsAt),
		EndsAt:      timeFromUnix(endsAt),
		AllDay:      allDay == 1,
		Timezone:    timezone,
		CreatedAt:   timeFromUnix(createdAt),
		UpdatedAt:   timeFromUnix(updatedAt),
	}
	if !value.Calendar.IsValid() {
		return appevents.EventWithVersion{}, fmt.Errorf("event %d: invalid stored calendar %q", id, calendar)
	}
	return appevents.EventWithVersion{Event: value, Version: version}, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
