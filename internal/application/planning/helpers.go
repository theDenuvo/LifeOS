package planning

import (
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// dateOf converts an instant to the local calendar date.
func dateOf(at time.Time, loc *time.Location) (task.CalendarDate, error) {
	local := at.In(loc)
	date, err := task.NewCalendarDate(local.Year(), local.Month(), local.Day())
	if err != nil {
		return task.CalendarDate{}, fmt.Errorf("invalid date for %s: %w", at.Format(time.RFC3339), err)
	}
	return date, nil
}

// parseDates reads ISO dates supplied by the interface.
func parseDates(values []string) ([]task.CalendarDate, error) {
	out := make([]task.CalendarDate, 0, len(values))
	for _, value := range values {
		date, err := task.ParseCalendarDate(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", err, value)
		}
		out = append(out, date)
	}
	return out, nil
}

// uniqueDates drops duplicates while keeping the order.
func uniqueDates(dates []task.CalendarDate) []task.CalendarDate {
	seen := make(map[string]bool, len(dates))
	out := make([]task.CalendarDate, 0, len(dates))
	for _, date := range dates {
		key := date.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, date)
	}
	return out
}

// addCalendarDays shifts a date by whole days.
func addCalendarDays(date task.CalendarDate, days int) task.CalendarDate {
	shifted := date.Time(time.UTC).AddDate(0, 0, days)
	built, err := task.NewCalendarDate(shifted.Year(), shifted.Month(), shifted.Day())
	if err != nil {
		return date
	}
	return built
}

// sameLocalDay reports whether the instant falls on the local calendar date.
func sameLocalDay(at time.Time, date task.CalendarDate, loc *time.Location) bool {
	if date.IsZero() {
		return false
	}
	local := at.In(loc)
	built, err := task.NewCalendarDate(local.Year(), local.Month(), local.Day())
	if err != nil {
		return false
	}
	return built.String() == date.String()
}

func sameCalendarDate(value *task.CalendarDate, date task.CalendarDate) bool {
	if value == nil || value.IsZero() {
		return false
	}
	return value.String() == date.String()
}

// repeatCovers reports whether a recurring task's flexible window covers date.
//
// The window starts on the occurrence date: a weekly habit planned for Monday with
// a two-day window may be done on Monday, Tuesday or Wednesday, after which the
// next occurrence is proposed. Without a planned date the occurrence is only
// proposed today, so the planner does not invent a whole history of a new habit.
func repeatCovers(repeat task.Repeat, planned *task.CalendarDate, date task.CalendarDate, today task.CalendarDate) bool {
	window := repeat.Window()
	if window <= 0 {
		return false
	}
	if planned == nil || planned.IsZero() {
		return date.String() == today.String()
	}
	if date.String() < planned.String() {
		return false
	}
	return date.String() <= addCalendarDays(*planned, window).String()
}

// deadlineKey renders a deadline for stable sorting.
func deadlineKey(date *task.CalendarDate) string {
	if date == nil || date.IsZero() {
		return "9999-12-31"
	}
	return date.String()
}

// cloneTask returns an independent copy, because Task.Update writes through its
// receiver.
func cloneTask(value *task.Task) *task.Task {
	if value == nil {
		return nil
	}
	copied := *value
	if value.Note != nil {
		note := *value.Note
		copied.Note = &note
	}
	if value.ExpectedResult != nil {
		result := *value.ExpectedResult
		copied.ExpectedResult = &result
	}
	if value.EstimateMinutes != nil {
		estimate := *value.EstimateMinutes
		copied.EstimateMinutes = &estimate
	}
	if value.Repeat != nil {
		repeat := *value.Repeat
		copied.Repeat = &repeat
	}
	if value.DueAt != nil {
		dueAt := *value.DueAt
		copied.DueAt = &dueAt
	}
	if value.CompletedAt != nil {
		completed := *value.CompletedAt
		copied.CompletedAt = &completed
	}
	if value.CancelledAt != nil {
		cancelled := *value.CancelledAt
		copied.CancelledAt = &cancelled
	}
	if value.PlannedDate != nil {
		planned := *value.PlannedDate
		copied.PlannedDate = &planned
	}
	if value.DueDate != nil {
		due := *value.DueDate
		copied.DueDate = &due
	}
	if value.ParentTaskID != nil {
		parent := *value.ParentTaskID
		copied.ParentTaskID = &parent
	}
	if value.RepeatParentID != nil {
		parent := *value.RepeatParentID
		copied.RepeatParentID = &parent
	}
	copied.Tags = append([]string(nil), value.Tags...)
	return &copied
}
