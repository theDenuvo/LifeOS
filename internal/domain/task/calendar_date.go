package task

import "time"

// CalendarDate represents a calendar date without time (YYYY-MM-DD).
//
// The zero value means "date not set". Because the type is exported, callers can
// construct an impossible date directly (for example February 30th), so every
// entry point validates it with IsValid instead of relying on time.Date silently
// normalising it.
//
// Day arithmetic is calendar based: one day forward always lands on the next
// local calendar day, which matters on DST days that are not 24 hours long.
type CalendarDate struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseCalendarDate parses a date string in YYYY-MM-DD format.
func ParseCalendarDate(s string) (CalendarDate, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return CalendarDate{}, ErrInvalidDate
	}
	return CalendarDate{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// NewCalendarDate creates a CalendarDate with validation.
func NewCalendarDate(year int, month time.Month, day int) (CalendarDate, error) {
	d := CalendarDate{Year: year, Month: month, Day: day}
	if !d.IsValid() {
		return CalendarDate{}, ErrInvalidDate
	}
	return d, nil
}

// IsZero reports whether the date is not set.
func (d CalendarDate) IsZero() bool {
	return d.Year == 0 && d.Month == 0 && d.Day == 0
}

// IsValid reports whether the calendar date is a real calendar day.
// The zero value is valid because it represents an unset date.
func (d CalendarDate) IsValid() bool {
	if d.IsZero() {
		return true
	}
	if d.Year < 1 || d.Year > 9999 {
		return false
	}
	if d.Month < time.January || d.Month > time.December {
		return false
	}
	if d.Day < 1 {
		return false
	}
	return d.Day <= daysInMonth(d.Year, d.Month)
}

// daysInMonth returns the number of days in the given month of the given year.
func daysInMonth(year int, month time.Month) int {
	switch month {
	case time.February:
		if isLeapYear(year) {
			return 29
		}
		return 28
	case time.April, time.June, time.September, time.November:
		return 30
	default:
		return 31
	}
}

// isLeapYear reports whether year is a leap year in the proleptic Gregorian
// calendar used by IANA time zones.
func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// String returns the date in YYYY-MM-DD format, or an empty string when unset
// or invalid. Invalid dates are never rendered as a rolled-over date.
func (d CalendarDate) String() string {
	if d.IsZero() || !d.IsValid() {
		return ""
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

// Time returns the local midnight of the date in the given location.
// The zero value is returned for unset or invalid dates.
func (d CalendarDate) Time(loc *time.Location) time.Time {
	if !d.IsValid() || d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// EndOfDay returns the exclusive end of the calendar day: the local midnight at
// the start of the next calendar day. The whole local day is therefore allowed
// and the task becomes overdue exactly at this instant.
//
// The next day is computed with calendar arithmetic, not by adding 24 hours, so
// DST days of 23 or 25 hours keep the same calendar date.
func (d CalendarDate) EndOfDay(loc *time.Location) time.Time {
	if !d.IsValid() || d.IsZero() {
		return time.Time{}
	}
	year, month, day, ok := addOneCalendarDay(d.Year, d.Month, d.Day)
	if !ok {
		// 9999-12-31 has no representable next day: saturate at the end of the
		// last supported local day instead of wrapping to year 0.
		return time.Date(9999, time.December, 31, 23, 59, 59, int(time.Second)-1, loc)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

// addOneCalendarDay advances year/month/day by one calendar day.
// It reports false when the date overflows the supported year range.
func addOneCalendarDay(year int, month time.Month, day int) (int, time.Month, int, bool) {
	if day < daysInMonth(year, month) {
		return year, month, day + 1, true
	}
	if month == time.December {
		if year >= 9999 {
			return 0, 0, 0, false
		}
		return year + 1, time.January, 1, true
	}
	return year, month + 1, 1, true
}
