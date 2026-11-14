package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Setting keys that describe how the user's time is arranged.
const (
	SettingWorkdayStart = "planning.workday_start"
	SettingWorkdayEnd   = "planning.workday_end"
	SettingWorkdayDays  = "planning.workday_days"
	SettingBuffer       = "planning.buffer_minutes"
	SettingDefaultEst   = "planning.default_estimate_minutes"
	SettingMinSlot      = "planning.min_slot_minutes"
)

// Default planning preferences.
//
// A working day of 09:00-19:00 on weekdays with a ten-minute buffer between
// blocks is a sane starting point: it is what most people mean by "plan my day"
// and it is easy to change in settings.
var (
	DefaultWorkdayStart = 9 * time.Hour
	DefaultWorkdayEnd   = 19 * time.Hour
	DefaultWorkdayDays  = []int{1, 2, 3, 4, 5}
	DefaultBuffer       = 10 * time.Minute
	DefaultEstimate     = 30 * time.Minute
	DefaultMinSlot      = 15 * time.Minute
)

// Preferences is the user's definition of available time.
type Preferences struct {
	Start           time.Duration // from local midnight
	End             time.Duration // from local midnight, exclusive
	Days            []int         // ISO weekdays, 1 = Monday
	Buffer          time.Duration // breathing room between blocks
	DefaultEstimate time.Duration
	MinSlot         time.Duration
}

// WindowOn returns the working window of a local date.
//
// A day that the user did not mark as a working day yields a zero interval and
// false: the planner then reports the day as unavailable instead of silently
// scheduling work into a weekend.
func (p Preferences) WindowOn(date task.CalendarDate, loc *time.Location) (Interval, bool) {
	if date.IsZero() || !date.IsValid() {
		return Interval{}, false
	}
	if !p.AllowsDate(date) {
		return Interval{}, false
	}
	start := date.Time(loc).Add(p.Start)
	end := date.Time(loc).Add(p.End)
	if !end.After(start) {
		return Interval{}, false
	}
	return Interval{Start: start, End: end}, true
}

// AllowsDate reports whether the date is one of the configured working days.
func (p Preferences) AllowsDate(date task.CalendarDate) bool {
	if len(p.Days) == 0 {
		return true
	}
	weekday := int(date.Time(time.UTC).Weekday())
	if weekday == 0 {
		weekday = 7
	}
	for _, day := range p.Days {
		if day == weekday {
			return true
		}
	}
	return false
}

// LoadPreferences reads the planning settings, falling back to defaults for any
// key the user has not set. Invalid values are ignored rather than fatal: a
// broken preference must not make planning impossible.
func (s *Service) LoadPreferences(ctx context.Context) (Preferences, error) {
	prefs := Preferences{
		Start:           DefaultWorkdayStart,
		End:             DefaultWorkdayEnd,
		Days:            append([]int(nil), DefaultWorkdayDays...),
		Buffer:          DefaultBuffer,
		DefaultEstimate: DefaultEstimate,
		MinSlot:         DefaultMinSlot,
	}

	var raw json.RawMessage
	var found bool
	var err error

	read := func(key string) (json.RawMessage, bool) {
		if err != nil {
			return nil, false
		}
		raw, found, err = s.readSetting(ctx, key)
		return raw, found
	}

	if value, ok := read(SettingWorkdayStart); ok {
		if minutes, parsed := parseClock(value); parsed {
			prefs.Start = minutes
		}
	}
	if value, ok := read(SettingWorkdayEnd); ok {
		if minutes, parsed := parseClock(value); parsed {
			prefs.End = minutes
		}
	}
	if value, ok := read(SettingWorkdayDays); ok {
		var days []int
		if json.Unmarshal(value, &days) == nil && len(days) > 0 {
			valid := make([]int, 0, len(days))
			for _, day := range days {
				if day >= 1 && day <= 7 {
					valid = append(valid, day)
				}
			}
			if len(valid) > 0 {
				sort.Ints(valid)
				prefs.Days = valid
			}
		}
	}
	if value, ok := read(SettingBuffer); ok {
		if minutes, parsed := parseInt(value); parsed && minutes >= 0 && minutes <= 120 {
			prefs.Buffer = time.Duration(minutes) * time.Minute
		}
	}
	if value, ok := read(SettingDefaultEst); ok {
		if minutes, parsed := parseInt(value); parsed && minutes >= 5 && minutes <= 480 {
			prefs.DefaultEstimate = time.Duration(minutes) * time.Minute
		}
	}
	if value, ok := read(SettingMinSlot); ok {
		if minutes, parsed := parseInt(value); parsed && minutes >= 5 && minutes <= 120 {
			prefs.MinSlot = time.Duration(minutes) * time.Minute
		}
	}
	if err != nil {
		return prefs, err
	}
	if prefs.End <= prefs.Start {
		prefs.Start, prefs.End = DefaultWorkdayStart, DefaultWorkdayEnd
	}
	return prefs, nil
}

// readSetting reads one setting inside its own transaction. Settings are read
// rarely (once per plan) and each call is cheap, so no long-lived transaction is
// opened just to gather preferences.
func (s *Service) readSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var value json.RawMessage
	var found bool
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		stored, ok, err := tx.Settings().Get(ctx, key)
		if err != nil {
			return err
		}
		value, found = stored, ok
		return nil
	})
	return value, found, err
}

// parseClock reads an "HH:MM" JSON string into an offset from midnight.
func parseClock(raw json.RawMessage) (time.Duration, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	parsed, err := time.Parse("15:04", text)
	if err != nil {
		return 0, false
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, true
}

func parseInt(raw json.RawMessage) (int, bool) {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

// formatClock renders an offset from midnight as HH:MM.
func formatClock(offset time.Duration) string {
	if offset < 0 {
		offset = 0
	}
	hours := int(offset / time.Hour)
	minutes := int((offset % time.Hour) / time.Minute)
	return fmt.Sprintf("%02d:%02d", hours, minutes)
}
