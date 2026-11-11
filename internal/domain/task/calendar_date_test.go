package task

import (
	"errors"
	"testing"
	"time"
)

var calendarFixedNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestCalendarDate_Parse(t *testing.T) {
	tests := []struct {
		input    string
		expected CalendarDate
		hasError bool
	}{
		{"2026-11-10", CalendarDate{Year: 2026, Month: 11, Day: 10}, false},
		{"2026-01-01", CalendarDate{Year: 2026, Month: 1, Day: 1}, false},
		{"2026-12-31", CalendarDate{Year: 2026, Month: 12, Day: 31}, false},
		{"2024-02-29", CalendarDate{Year: 2024, Month: 2, Day: 29}, false},
		{"invalid", CalendarDate{}, true},
		{"2026/11/10", CalendarDate{}, true},
		{"", CalendarDate{}, true},
		{"2026-02-30", CalendarDate{}, true},
		{"2026-13-01", CalendarDate{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			date, err := ParseCalendarDate(tt.input)
			if tt.hasError {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if date != tt.expected {
				t.Errorf("got %v, want %v", date, tt.expected)
			}
		})
	}
}

// TestCalendarDate_IsValid covers every impossible date class named in the review.
func TestCalendarDate_IsValid(t *testing.T) {
	tests := []struct {
		name string
		date CalendarDate
		want bool
	}{
		{"zero value", CalendarDate{}, true},
		{"30 february", CalendarDate{Year: 2026, Month: 2, Day: 30}, false},
		{"31 april", CalendarDate{Year: 2026, Month: 4, Day: 31}, false},
		{"31 june", CalendarDate{Year: 2026, Month: 6, Day: 31}, false},
		{"31 september", CalendarDate{Year: 2026, Month: 9, Day: 31}, false},
		{"31 november", CalendarDate{Year: 2026, Month: 11, Day: 31}, false},
		{"month 0", CalendarDate{Year: 2026, Month: 0, Day: 10}, false},
		{"month 13", CalendarDate{Year: 2026, Month: 13, Day: 10}, false},
		{"day 0", CalendarDate{Year: 2026, Month: 5, Day: 0}, false},
		{"negative day", CalendarDate{Year: 2026, Month: 5, Day: -1}, false},
		{"negative month", CalendarDate{Year: 2026, Month: -1, Day: 10}, false},
		{"negative year", CalendarDate{Year: -2026, Month: 5, Day: 10}, false},
		{"year 0 month set", CalendarDate{Year: 0, Month: 5, Day: 10}, false},
		{"leap day valid 2024", CalendarDate{Year: 2024, Month: 2, Day: 29}, true},
		{"leap day valid 2000", CalendarDate{Year: 2000, Month: 2, Day: 29}, true},
		{"leap day invalid 2026", CalendarDate{Year: 2026, Month: 2, Day: 29}, false},
		{"leap day invalid 1900", CalendarDate{Year: 1900, Month: 2, Day: 29}, false},
		{"29 feb non-leap century 2100", CalendarDate{Year: 2100, Month: 2, Day: 29}, false},
		{"31 december valid", CalendarDate{Year: 2026, Month: 12, Day: 31}, true},
		{"30 february leap year", CalendarDate{Year: 2024, Month: 2, Day: 30}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.date.IsValid(); got != tt.want {
				t.Errorf("CalendarDate%+v.IsValid() = %v, want %v", tt.date, got, tt.want)
			}
		})
	}
}

func TestCalendarDate_NewCalendarDate(t *testing.T) {
	if _, err := NewCalendarDate(2026, 2, 30); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("expected ErrInvalidDate for 30 February, got %v", err)
	}
	got, err := NewCalendarDate(2026, 2, 28)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (CalendarDate{Year: 2026, Month: 2, Day: 28}) {
		t.Errorf("got %v", got)
	}
}

// TestCalendarDate_NoSilentNormalisation is the core regression test: a directly
// constructed impossible date must never be quietly rolled forward.
func TestCalendarDate_NoSilentNormalisation(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	impossible := CalendarDate{Year: 2026, Month: 2, Day: 30}

	if got := impossible.Time(loc); !got.IsZero() {
		t.Errorf("Time() = %v, want zero time for an invalid date (no normalisation)", got)
	}
	if got := impossible.EndOfDay(loc); !got.IsZero() {
		t.Errorf("EndOfDay() = %v, want zero time for an invalid date", got)
	}

	// String must not present a rolled-over date as if it were real.
	if got := impossible.String(); got != "" {
		t.Errorf("String() = %q, want empty string for an invalid date (no normalisation)", got)
	}
}

func TestCalendarDate_String(t *testing.T) {
	date := CalendarDate{Year: 2026, Month: 11, Day: 10}
	if date.String() != "2026-11-10" {
		t.Errorf("String() = %q, want %q", date.String(), "2026-11-10")
	}

	var zero CalendarDate
	if zero.String() != "" {
		t.Errorf("zero date String() = %q, want empty", zero.String())
	}
}

func TestCalendarDate_IsZero(t *testing.T) {
	var zero CalendarDate
	if !zero.IsZero() {
		t.Error("zero date should be IsZero()")
	}
	if (CalendarDate{Year: 2026, Month: 1, Day: 1}).IsZero() {
		t.Error("non-zero date should not be IsZero()")
	}
}

func TestCalendarDate_Time(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}
	date := CalendarDate{Year: 2026, Month: 11, Day: 10}
	tm := date.Time(loc)

	if tm.Year() != 2026 || tm.Month() != 11 || tm.Day() != 10 {
		t.Errorf("Time() = %v, want 2026-11-10 00:00:00", tm)
	}
	if tm.Hour() != 0 || tm.Minute() != 0 || tm.Second() != 0 {
		t.Error("Time() should be at midnight")
	}
	if tm.Location() != loc {
		t.Error("Time() should be in the provided location")
	}

	var zero CalendarDate
	if !zero.Time(loc).IsZero() {
		t.Error("zero date Time() should be zero")
	}
}

// TestCalendarDate_EndOfDay covers the corrected boundary: the exclusive end of
// the local calendar day, i.e. the next local midnight.
func TestCalendarDate_EndOfDay(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	date := CalendarDate{Year: 2026, Month: 11, Day: 10}
	end := date.EndOfDay(loc)

	want := time.Date(2026, 11, 11, 0, 0, 0, 0, loc)
	if !end.Equal(want) {
		t.Errorf("EndOfDay() = %v, want %v (next local midnight)", end, want)
	}
	if end.Location() != loc {
		t.Error("EndOfDay() should be in the provided location")
	}

	// The whole local day is allowed: one second before the boundary is fine.
	if !date.Time(loc).Add(24*time.Hour - time.Second).Before(end) {
		t.Error("one second before the boundary must be before EndOfDay")
	}

	var zero CalendarDate
	if !zero.EndOfDay(loc).IsZero() {
		t.Error("zero date EndOfDay() should be zero")
	}
}

func TestCalendarDate_EndOfDay_MonthAndYearBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("UTC")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	tests := []struct {
		date CalendarDate
		want time.Time
	}{
		{CalendarDate{Year: 2026, Month: 1, Day: 31}, time.Date(2026, 2, 1, 0, 0, 0, 0, loc)},
		{CalendarDate{Year: 2026, Month: 12, Day: 31}, time.Date(2027, 1, 1, 0, 0, 0, 0, loc)},
		{CalendarDate{Year: 2024, Month: 2, Day: 28}, time.Date(2024, 2, 29, 0, 0, 0, 0, loc)},
		{CalendarDate{Year: 2024, Month: 2, Day: 29}, time.Date(2024, 3, 1, 0, 0, 0, 0, loc)},
	}

	for _, tt := range tests {
		if got := tt.date.EndOfDay(loc); !got.Equal(tt.want) {
			t.Errorf("%v.EndOfDay() = %v, want %v", tt.date, got, tt.want)
		}
	}
}

// TestCalendarDate_DSTTransitions covers the real America/New_York transitions of
// 2026, where the local day is not 24 hours long.
func TestCalendarDate_DSTTransitions(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	t.Run("spring forward 2026-03-08", func(t *testing.T) {
		date := CalendarDate{Year: 2026, Month: 3, Day: 8}

		if !date.IsValid() {
			t.Fatal("2026-03-08 must be a valid calendar date")
		}
		if got := date.String(); got != "2026-03-08" {
			t.Errorf("String() = %q, want 2026-03-08", got)
		}

		start := date.Time(loc)
		if start.Hour() != 0 || start.Minute() != 0 {
			t.Errorf("Time() = %v, want local midnight", start)
		}
		if got := start.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-03-08T05:00:00Z" {
			t.Errorf("local midnight in UTC = %s, want 2026-03-08T05:00:00Z (EST = UTC-5)", got)
		}

		end := date.EndOfDay(loc)
		wantEnd := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
		if !end.Equal(wantEnd) {
			t.Errorf("EndOfDay() = %v, want %v", end, wantEnd)
		}
		if got := end.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-03-09T04:00:00Z" {
			t.Errorf("next local midnight in UTC = %s, want 2026-03-09T04:00:00Z (EDT = UTC-4)", got)
		}

		// The local day is 23 hours long, not 24.
		if got := end.Sub(start); got != 23*time.Hour {
			t.Errorf("local day length = %v, want 23h (spring forward)", got)
		}
		if end.Sub(start) == 24*time.Hour {
			t.Error("spring-forward day must not be measured as 24 hours")
		}

		// The calendar date must not shift because of the transition.
		if end.UTC().Add(-time.Nanosecond).In(loc).Day() != 8 {
			t.Error("an instant just before the boundary must still be on the due local day")
		}

		// Overdue boundary: one nanosecond before is fine, at the boundary it is overdue.
		task := &Task{
			Title:    "dst spring",
			Priority: PriorityMedium,
			Status:   StatusOpen,
			Timezone: "America/New_York",
			Tags:     []string{},
		}
		if err := task.Update(calendarFixedNow, WithDueDate(date)); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		if task.IsOverdue(end.Add(-time.Nanosecond)) {
			t.Error("one nanosecond before next local midnight must not be overdue")
		}
		if !task.IsOverdue(end) {
			t.Error("exactly at next local midnight must be overdue")
		}
		if !task.IsOverdue(end.Add(time.Hour)) {
			t.Error("after next local midnight must be overdue")
		}

		overdueAt := task.OverdueAt()
		if overdueAt == nil || !overdueAt.Equal(end) {
			t.Errorf("OverdueAt() = %v, want %v", overdueAt, end)
		}
	})

	t.Run("fall back 2026-11-01", func(t *testing.T) {
		date := CalendarDate{Year: 2026, Month: 11, Day: 1}

		if !date.IsValid() {
			t.Fatal("2026-11-01 must be a valid calendar date")
		}
		if got := date.String(); got != "2026-11-01" {
			t.Errorf("String() = %q, want 2026-11-01", got)
		}

		start := date.Time(loc)
		if got := start.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-11-01T04:00:00Z" {
			t.Errorf("local midnight in UTC = %s, want 2026-11-01T04:00:00Z (EDT = UTC-4)", got)
		}

		end := date.EndOfDay(loc)
		wantEnd := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
		if !end.Equal(wantEnd) {
			t.Errorf("EndOfDay() = %v, want %v", end, wantEnd)
		}
		if got := end.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-11-02T05:00:00Z" {
			t.Errorf("next local midnight in UTC = %s, want 2026-11-02T05:00:00Z (EST = UTC-5)", got)
		}

		// The local day is 25 hours long, not 24.
		if got := end.Sub(start); got != 25*time.Hour {
			t.Errorf("local day length = %v, want 25h (fall back)", got)
		}

		task := &Task{
			Title:    "dst fall",
			Priority: PriorityMedium,
			Status:   StatusOpen,
			Timezone: "America/New_York",
			Tags:     []string{},
		}
		if err := task.Update(calendarFixedNow, WithDueDate(date)); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		if task.IsOverdue(end.Add(-time.Nanosecond)) {
			t.Error("one nanosecond before next local midnight must not be overdue")
		}
		if !task.IsOverdue(end) {
			t.Error("exactly at next local midnight must be overdue")
		}

		// An instant inside the repeated hour is still on the due local day.
		repeated := time.Date(2026, 11, 1, 1, 30, 0, 0, loc)
		if task.IsOverdue(repeated) {
			t.Error("an instant inside the repeated hour must not be overdue")
		}

		overdueAt := task.OverdueAt()
		if overdueAt == nil || !overdueAt.Equal(end) {
			t.Errorf("OverdueAt() = %v, want %v", overdueAt, end)
		}
	})

	t.Run("no fixed 24 hour arithmetic", func(t *testing.T) {
		// A 25-hour local day must not be shortened by naive 24h arithmetic.
		date := CalendarDate{Year: 2026, Month: 11, Day: 1}
		start := date.Time(loc)
		end := date.EndOfDay(loc)

		naive := start.Add(24 * time.Hour)
		if naive.Equal(end) {
			t.Fatal("EndOfDay must not be computed as start + 24h")
		}
		if !naive.Before(end) {
			t.Errorf("naive 24h boundary %v is not before the real boundary %v", naive, end)
		}
	})
}
