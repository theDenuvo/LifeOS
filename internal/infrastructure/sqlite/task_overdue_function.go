package sqlite

import (
	"database/sql/driver"
	"strings"
	"sync"
	"time"

	gosqlite "modernc.org/sqlite"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Registering a Go scalar function lets the overdue filter run fully inside
// SQL: predicate evaluation, COUNT and LIMIT/OFFSET stay in the database and
// only the requested page materialises rows. The function mirrors
// task.Task.IsOverdue exactly — the domain rule is written once there, and
// this bridge must not drift from it.
var registerOverdueFunction sync.Once

// registerTaskOverdueFunction registers the deterministic scalar function
//
//	lifeos_task_overdue(status, due_date, due_at, timezone, now) -> 0/1
//
// Semantics (see Task.IsOverdue and CalendarDate.EndOfDay):
//   - only an open task can be overdue;
//   - with an exact due_at, the boundary instant itself is still on time;
//   - with a calendar due_date, the whole local day is allowed and the task
//     becomes overdue at the start of the next local day;
//   - no deadline means never overdue.
func registerTaskOverdueFunction() {
	registerOverdueFunction.Do(func() {
		_ = gosqlite.RegisterDeterministicScalarFunction("lifeos_task_overdue", 5, overdueFunction)
	})
}

func overdueFunction(_ *gosqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 5 {
		return nil, nil
	}
	status, _ := args[0].(string)
	if status != string(task.StatusOpen) {
		return int64(0), nil
	}

	nowValue, _ := args[4].(int64)
	now := time.Unix(nowValue, 0).UTC()

	if dueAtValue, ok := nullableInt64(args[2]); ok {
		// IsOverdue: now.After(*t.DueAt) — the boundary instant is on time.
		return boolValue(now.After(time.Unix(dueAtValue, 0).UTC())), nil
	}

	year, month, day, hasDate := calendarDateValue(args[1])
	if !hasDate {
		return int64(0), nil
	}
	loc, err := time.LoadLocation(strings.TrimSpace(overdueTimezone(args[3])))
	if err != nil {
		// The domain treats an unusable timezone as "no boundary": not overdue.
		return int64(0), nil
	}

	var boundary time.Time
	if year == 9999 && month == time.December && day == 31 {
		// CalendarDate.EndOfDay saturates the last supported day.
		boundary = time.Date(9999, time.December, 31, 23, 59, 59, int(time.Second)-1, loc)
	} else {
		boundary = time.Date(year, month, day+1, 0, 0, 0, 0, loc)
	}
	// IsOverdue: !now.Before(boundary) — the boundary itself is overdue.
	return boolValue(!now.Before(boundary)), nil
}

// calendarDateValue decodes a due_date cell. The column is declared DATE, so
// the driver may hand back either the stored text or a time.Time; both carry
// the same timezone-neutral calendar day.
func calendarDateValue(value driver.Value) (year int, month time.Month, day int, ok bool) {
	switch v := value.(type) {
	case nil:
		return 0, 0, 0, false
	case string:
		return parseCalendarDay(v)
	case []byte:
		return parseCalendarDay(string(v))
	case time.Time:
		if v.IsZero() {
			return 0, 0, 0, false
		}
		t := v
		if t.Location() != time.UTC {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		return t.Year(), t.Month(), t.Day(), true
	default:
		return 0, 0, 0, false
	}
}

func parseCalendarDay(value string) (int, time.Month, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, 0, false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0, 0, 0, false
	}
	return parsed.Year(), parsed.Month(), parsed.Day(), true
}

func nullableInt64(value driver.Value) (int64, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func overdueTimezone(value driver.Value) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

func boolValue(b bool) driver.Value {
	if b {
		return int64(1)
	}
	return int64(0)
}
