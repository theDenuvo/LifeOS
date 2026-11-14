package event_test

import (
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/event"
)

func TestNewEventValidates(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	start := now.Add(2 * time.Hour)
	end := start.Add(time.Hour)
	description := "  Обсудить квартал  "
	location := "  Zoom  "

	value, err := event.NewEvent("  Синк с командой  ", &description, &location, event.CalendarWork, start, end, false, "Europe/Moscow", now)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if value.Title != "Синк с командой" {
		t.Errorf("title not trimmed: %q", value.Title)
	}
	if value.Description == nil || *value.Description != "Обсудить квартал" {
		t.Errorf("description not normalized: %v", value.Description)
	}
	if value.Location == nil || *value.Location != "Zoom" {
		t.Errorf("location not normalized: %v", value.Location)
	}

	cases := []struct {
		name     string
		title    string
		calendar event.Calendar
		start    time.Time
		end      time.Time
		want     error
	}{
		{"empty title", "  ", event.CalendarWork, start, end, event.ErrInvalidTitle},
		{"unknown calendar", "Событие", event.Calendar("other"), start, end, event.ErrInvalidCalendar},
		{"inverted range", "Событие", event.CalendarWork, end, start, event.ErrInvalidRange},
		{"zero start", "Событие", event.CalendarWork, time.Time{}, end, event.ErrInvalidRange},
		{"too long", "Отпуск", event.CalendarPersonal, start, start.AddDate(0, 2, 0), event.ErrRangeTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := event.NewEvent(tc.title, nil, nil, tc.calendar, tc.start, tc.end, false, "Europe/Moscow", now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
