package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Recurrence validation errors.
var (
	// ErrInvalidRepeat is returned when a recurrence rule is malformed.
	ErrInvalidRepeat = errors.New("invalid repeat rule")
)

// RepeatFreq is how often a recurring task comes back.
type RepeatFreq string

// Supported recurrence frequencies.
const (
	RepeatDaily   RepeatFreq = "daily"
	RepeatWeekly  RepeatFreq = "weekly"
	RepeatMonthly RepeatFreq = "monthly"
)

// IsValid reports whether the frequency is known.
func (f RepeatFreq) IsValid() bool {
	switch f {
	case RepeatDaily, RepeatWeekly, RepeatMonthly:
		return true
	default:
		return false
	}
}

// Bounds for a recurrence rule.
const (
	minRepeatInterval = 1
	maxRepeatInterval = 12
	maxRepeatWindow   = 7
)

// Repeat describes a recurring task.
//
// A recurring task is a template: completing an occurrence creates the next one,
// so the completed instance stays in the history. WindowDays is how far the
// planner may move a single occurrence without breaking the habit — this is what
// makes intervals flexible instead of rigid.
type Repeat struct {
	Freq       RepeatFreq
	Interval   int           // every N days/weeks/months, 1-12
	Weekdays   []int         // weekly only: 1=Monday … 7=Sunday; empty means "same weekday as the anchor"
	Until      *CalendarDate // optional last date; occurrences after it are not created
	WindowDays int           // 0-7 days the planner may shift an occurrence
}

// Validate checks that the rule is self-consistent.
func (r Repeat) Validate() error {
	if !r.Freq.IsValid() {
		return fmt.Errorf("%w: freq must be daily, weekly, or monthly", ErrInvalidRepeat)
	}
	if r.Interval < minRepeatInterval || r.Interval > maxRepeatInterval {
		return fmt.Errorf("%w: interval must be %d-%d", ErrInvalidRepeat, minRepeatInterval, maxRepeatInterval)
	}
	if r.WindowDays < 0 || r.WindowDays > maxRepeatWindow {
		return fmt.Errorf("%w: window_days must be 0-%d", ErrInvalidRepeat, maxRepeatWindow)
	}
	if len(r.Weekdays) > 0 {
		if r.Freq != RepeatWeekly {
			return fmt.Errorf("%w: weekdays are only allowed for weekly rules", ErrInvalidRepeat)
		}
		if len(r.Weekdays) > 7 {
			return fmt.Errorf("%w: at most 7 weekdays", ErrInvalidRepeat)
		}
		if r.Interval != 1 {
			return fmt.Errorf("%w: weekdays require interval 1; use several weekdays instead", ErrInvalidRepeat)
		}
		seen := map[int]bool{}
		for _, day := range r.Weekdays {
			if day < 1 || day > 7 {
				return fmt.Errorf("%w: weekday must be 1-7", ErrInvalidRepeat)
			}
			if seen[day] {
				return fmt.Errorf("%w: weekday %d is repeated", ErrInvalidRepeat, day)
			}
			seen[day] = true
		}
	}
	if r.Until != nil && !r.Until.IsZero() && !r.Until.IsValid() {
		return fmt.Errorf("%w: until must be a calendar date", ErrInvalidRepeat)
	}
	return nil
}

// MarshalJSON stores the rule in the compact form used by the tasks table.
func (r Repeat) MarshalJSON() ([]byte, error) {
	type wire struct {
		Freq       RepeatFreq `json:"freq"`
		Interval   int        `json:"interval"`
		Weekdays   []int      `json:"weekdays,omitempty"`
		Until      string     `json:"until,omitempty"`
		WindowDays int        `json:"window_days,omitempty"`
	}
	out := wire{Freq: r.Freq, Interval: r.Interval, Weekdays: r.Weekdays, WindowDays: r.WindowDays}
	if r.Until != nil && !r.Until.IsZero() {
		out.Until = r.Until.String()
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the compact form and validates it.
func (r *Repeat) UnmarshalJSON(data []byte) error {
	type wire struct {
		Freq       RepeatFreq `json:"freq"`
		Interval   int        `json:"interval"`
		Weekdays   []int      `json:"weekdays,omitempty"`
		Until      string     `json:"until,omitempty"`
		WindowDays int        `json:"window_days,omitempty"`
	}
	var in wire
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRepeat, err)
	}
	interval := in.Interval
	if interval == 0 {
		interval = 1
	}
	parsed := Repeat{Freq: in.Freq, Interval: interval, Weekdays: in.Weekdays, WindowDays: in.WindowDays}
	if in.Until != "" {
		until, err := ParseCalendarDate(in.Until)
		if err != nil {
			return fmt.Errorf("%w: until must be YYYY-MM-DD", ErrInvalidRepeat)
		}
		parsed.Until = &until
	}
	if err := parsed.Validate(); err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Next returns the first occurrence strictly after anchor.
//
// The anchor is the date of the occurrence that was just completed, so the next
// date follows the rule from there. When the rule has an end date and the next
// occurrence falls after it, the zero CalendarDate is returned and the series
// stops.
func (r Repeat) Next(anchor CalendarDate) (CalendarDate, error) {
	if err := r.Validate(); err != nil {
		return CalendarDate{}, err
	}
	if anchor.IsZero() || !anchor.IsValid() {
		return CalendarDate{}, ErrInvalidRepeat
	}

	var next CalendarDate
	switch r.Freq {
	case RepeatDaily:
		next = addDays(anchor, r.Interval)
	case RepeatWeekly:
		if len(r.Weekdays) == 0 {
			next = addDays(anchor, 7*r.Interval)
			break
		}
		// Walk forward to the next allowed weekday. The loop is bounded by a
		// week because the set is never empty here.
		candidate := anchor
		for i := 0; i < 8; i++ {
			candidate = addDays(candidate, 1)
			if r.allows(candidate) {
				next = candidate
				break
			}
		}
	case RepeatMonthly:
		next = addMonths(anchor, r.Interval)
	}
	if next.IsZero() || !next.IsValid() {
		return CalendarDate{}, ErrInvalidRepeat
	}

	if r.Until != nil && !r.Until.IsZero() {
		if next.String() > r.Until.String() {
			return CalendarDate{}, nil
		}
	}
	return next, nil
}

// Allows reports whether date satisfies the recurrence pattern.
func (r Repeat) Allows(date CalendarDate) bool {
	if err := r.Validate(); err != nil {
		return false
	}
	if date.IsZero() || !date.IsValid() {
		return false
	}
	if r.Freq == RepeatWeekly && len(r.Weekdays) > 0 {
		return r.allows(date)
	}
	return true
}

func (r Repeat) allows(date CalendarDate) bool {
	if len(r.Weekdays) == 0 {
		return true
	}
	index := int(date.Time(time.UTC).Weekday())
	if index == 0 {
		index = 7
	}
	for _, day := range r.Weekdays {
		if day == index {
			return true
		}
	}
	return false
}

// Window returns how far the planner may shift one occurrence.
func (r Repeat) Window() int {
	if r.WindowDays < 0 {
		return 0
	}
	if r.WindowDays > maxRepeatWindow {
		return maxRepeatWindow
	}
	return r.WindowDays
}

// Describe renders the rule for the interface and the assistant.
func (r Repeat) Describe() string {
	if err := r.Validate(); err != nil {
		return ""
	}
	switch r.Freq {
	case RepeatDaily:
		if r.Interval == 1 {
			return "каждый день"
		}
		return fmt.Sprintf("каждые %d дн.", r.Interval)
	case RepeatWeekly:
		if len(r.Weekdays) > 0 {
			return "по " + weekdayNames(r.Weekdays)
		}
		if r.Interval == 1 {
			return "каждую неделю"
		}
		return fmt.Sprintf("каждые %d нед.", r.Interval)
	case RepeatMonthly:
		if r.Interval == 1 {
			return "каждый месяц"
		}
		return fmt.Sprintf("каждые %d мес.", r.Interval)
	}
	return ""
}

func weekdayNames(days []int) string {
	names := [...]string{"", "пн", "вт", "ср", "чт", "пт", "сб", "вс"}
	parts := make([]string, 0, len(days))
	for _, day := range days {
		if day >= 1 && day <= 7 {
			parts = append(parts, names[day])
		}
	}
	out := ""
	for i, part := range parts {
		switch {
		case i == 0:
			out = part
		case i == len(parts)-1:
			out += " и " + part
		default:
			out += ", " + part
		}
	}
	return out
}

// addDays shifts a calendar date by whole days.
func addDays(date CalendarDate, days int) CalendarDate {
	shifted := date.Time(time.UTC).AddDate(0, 0, days)
	return CalendarDate{Year: shifted.Year(), Month: shifted.Month(), Day: shifted.Day()}
}

// addMonths shifts a calendar date by whole months, clamping the day to the last
// day of the target month so that 31 January + 1 month is 28/29 February.
//
// Clamping happens once, at the end: advancing month by month and clamping at
// every step would make a monthly task drift from the 31st to the 28th and stay
// there forever.
func addMonths(date CalendarDate, months int) CalendarDate {
	year, month := date.Year, date.Month
	day := date.Day
	step := 1
	if months < 0 {
		step = -1
		months = -months
	}
	for i := 0; i < months; i++ {
		if step > 0 {
			if month == time.December {
				month = time.January
				year++
			} else {
				month++
			}
			continue
		}
		if month == time.January {
			month = time.December
			year--
		} else {
			month--
		}
	}
	if last := daysInMonth(year, month); day > last {
		day = last
	}
	return CalendarDate{Year: year, Month: month, Day: day}
}
