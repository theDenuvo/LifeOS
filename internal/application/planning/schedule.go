package planning

import (
	"sort"
	"strconv"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Interval is a half-open time range [Start, End).
type Interval struct {
	Start time.Time
	End   time.Time
}

// Duration returns the length of the interval.
func (i Interval) Duration() time.Duration {
	if i.End.Before(i.Start) {
		return 0
	}
	return i.End.Sub(i.Start)
}

// IsZero reports whether the interval carries no time.
func (i Interval) IsZero() bool { return i.Start.IsZero() || !i.End.After(i.Start) }

// Overlaps reports whether two intervals share any instant.
func (i Interval) Overlaps(other Interval) bool {
	return i.Start.Before(other.End) && other.Start.Before(i.End)
}

// Contains reports whether the instant lies inside the interval.
func (i Interval) Contains(at time.Time) bool {
	return !at.Before(i.Start) && at.Before(i.End)
}

// Busy is an occupied range with a human label, so the interface can explain why
// a slot is not free.
type Busy struct {
	Interval
	Label   string
	Kind    string // "event" or "block"
	BlockID int64
	TaskID  int64
}

// Candidate is a task the planner may schedule.
type Candidate struct {
	TaskID      int64
	Title       string
	Priority    task.Priority
	Estimate    time.Duration
	DueAt       *time.Time
	DueDate     *task.CalendarDate
	PlannedDate *task.CalendarDate
	Recurring   bool
	Projected   bool // window of a recurring task, not an exact planned date
	CreatedAt   time.Time
	// Urgency is set by the service: 0 overdue, 1 due today, 2 due soon,
	// 3 planned for the day, 4 without a deadline.
	Urgency int
}

// Placement is one scheduled candidate.
type Placement struct {
	TaskID   int64
	Title    string
	Priority task.Priority
	Start    time.Time
	End      time.Time
}

// Unplaced is a candidate that could not be scheduled, with the reason shown to
// the user instead of a silent omission.
type Unplaced struct {
	TaskID   int64
	Title    string
	Estimate time.Duration
	Reason   string
}

// FreeSlots subtracts busy ranges from the window.
//
// The buffer is applied inside the boundaries of each free slot, so a planned
// block never runs into an appointment, and MinSlot drops fragments that are too
// short to be worth placing.
func FreeSlots(window Interval, busy []Busy, buffer, minSlot time.Duration) []Interval {
	if window.IsZero() {
		return nil
	}
	clipped := make([]Interval, 0, len(busy))
	for _, item := range busy {
		start := item.Start
		if start.Before(window.Start) {
			start = window.Start
		}
		end := item.End
		if end.After(window.End) {
			end = window.End
		}
		if !end.After(start) {
			continue
		}
		clipped = append(clipped, Interval{Start: start, End: end})
	}
	sort.Slice(clipped, func(a, b int) bool { return clipped[a].Start.Before(clipped[b].Start) })

	merged := make([]Interval, 0, len(clipped))
	for _, item := range clipped {
		if len(merged) == 0 {
			merged = append(merged, item)
			continue
		}
		last := &merged[len(merged)-1]
		if !item.Start.After(last.End) {
			if item.End.After(last.End) {
				last.End = item.End
			}
			continue
		}
		merged = append(merged, item)
	}

	free := make([]Interval, 0, len(merged)+1)
	cursor := window.Start
	for _, item := range merged {
		if item.Start.After(cursor) {
			free = append(free, Interval{Start: cursor, End: item.Start})
		}
		if item.End.After(cursor) {
			cursor = item.End
		}
	}
	if window.End.After(cursor) {
		free = append(free, Interval{Start: cursor, End: window.End})
	}

	out := make([]Interval, 0, len(free))
	for _, slot := range free {
		start := slot.Start.Add(buffer)
		end := slot.End.Add(-buffer)
		if !end.After(start) {
			continue
		}
		if end.Sub(start) < minSlot {
			continue
		}
		out = append(out, Interval{Start: start, End: end})
	}
	return out
}

// Schedule fills the free slots with candidates in urgency order.
//
// Order matters more than packing density: an overdue high-priority task takes
// the first morning slot even if a smaller task would fit better. The buffer is
// kept between two blocks placed in the same slot, so the plan stays realistic.
func Schedule(free []Interval, candidates []Candidate, buffer time.Duration) ([]Placement, []Unplaced) {
	slots := make([]Interval, len(free))
	copy(slots, free)

	ordered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Estimate > 0 {
			ordered = append(ordered, candidate)
		}
	}
	sort.SliceStable(ordered, func(a, b int) bool {
		left, right := ordered[a], ordered[b]
		if left.Urgency != right.Urgency {
			return left.Urgency < right.Urgency
		}
		if left.Priority != right.Priority {
			return priorityRank(left.Priority) < priorityRank(right.Priority)
		}
		if leftDue, rightDue := dueInstant(left), dueInstant(right); !leftDue.Equal(rightDue) {
			return leftDue.Before(rightDue)
		}
		if left.Estimate != right.Estimate {
			return left.Estimate > right.Estimate
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	placements := make([]Placement, 0, len(ordered))
	unplaced := make([]Unplaced, 0)
	for _, candidate := range ordered {
		index := -1
		for i, slot := range slots {
			if slot.Duration() >= candidate.Estimate {
				index = i
				break
			}
		}
		if index < 0 {
			unplaced = append(unplaced, Unplaced{
				TaskID:   candidate.TaskID,
				Title:    candidate.Title,
				Estimate: candidate.Estimate,
				Reason:   noRoomReason(candidate, slots),
			})
			continue
		}
		slot := slots[index]
		start := slot.Start
		end := start.Add(candidate.Estimate)
		placements = append(placements, Placement{
			TaskID:   candidate.TaskID,
			Title:    candidate.Title,
			Priority: candidate.Priority,
			Start:    start,
			End:      end,
		})
		remaining := Interval{Start: end.Add(buffer), End: slot.End}
		if remaining.Duration() > 0 {
			slots[index] = remaining
		} else {
			slots = append(slots[:index], slots[index+1:]...)
		}
	}
	return placements, unplaced
}

func priorityRank(priority task.Priority) int {
	switch priority {
	case task.PriorityHigh:
		return 0
	case task.PriorityMedium:
		return 1
	default:
		return 2
	}
}

func dueInstant(candidate Candidate) time.Time {
	if candidate.DueAt != nil {
		return *candidate.DueAt
	}
	if candidate.DueDate != nil {
		return candidate.DueDate.Time(time.UTC)
	}
	return time.Time{}
}

func noRoomReason(candidate Candidate, slots []Interval) string {
	total := time.Duration(0)
	for _, slot := range slots {
		total += slot.Duration()
	}
	if total == 0 {
		return "в этот день больше нет свободного времени"
	}
	if total >= candidate.Estimate {
		return "нужно непрерывное окно " + formatDuration(candidate.Estimate) + ", а свободное время разбито на части"
	}
	return "свободного времени меньше, чем нужно (" + formatDuration(total) + " из " + formatDuration(candidate.Estimate) + ")"
}

// formatDuration renders a duration as minutes or hours for user-facing text.
func formatDuration(value time.Duration) string {
	minutes := int(value.Minutes() + 0.5)
	if minutes < 60 {
		return strconv.Itoa(minutes) + " мин"
	}
	hours := minutes / 60
	rest := minutes % 60
	if rest == 0 {
		return strconv.Itoa(hours) + " ч"
	}
	return strconv.Itoa(hours) + " ч " + strconv.Itoa(rest) + " мин"
}
