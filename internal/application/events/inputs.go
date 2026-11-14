package events

import "time"

// CreateEventInput is the typed input for creating an event.
//
// EndsAt is optional: when omitted the service uses +1 hour, or +1 day for an
// all-day event. Calendar defaults to personal, timezone to the service default.
type CreateEventInput struct {
	Title       string
	Description *string
	Location    *string
	Calendar    string
	StartsAt    time.Time
	EndsAt      *time.Time
	AllDay      bool
	Timezone    *string
}

// UpdateEventInput is a partial update. Unset fields keep their current value;
// an empty description or location clears the field.
type UpdateEventInput struct {
	Title       *string
	Description *string
	Location    *string
	Calendar    *string
	StartsAt    *time.Time
	EndsAt      *time.Time
	AllDay      *bool
	Timezone    *string
}

// ListRangeResult is a calendar range read.
type ListRangeResult struct {
	Events []EventWithVersion
}
