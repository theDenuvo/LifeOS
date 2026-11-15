package planning

import (
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// ReminderKind classifies why the user is being reminded.
type ReminderKind string

// Reminder kinds.
const (
	// ReminderLead is an advance warning that gives the user time to start.
	ReminderLead ReminderKind = "lead"
	// ReminderDue is the reminder at the deadline itself.
	ReminderDue ReminderKind = "due"
	// ReminderOverdue is the follow-up after the deadline has passed.
	ReminderOverdue ReminderKind = "overdue"
)

// IsValid reports whether the kind is known.
func (k ReminderKind) IsValid() bool {
	switch k {
	case ReminderLead, ReminderDue, ReminderOverdue:
		return true
	default:
		return false
	}
}

// ReminderStatus is the delivery state of a reminder.
type ReminderStatus string

// Reminder states.
const (
	ReminderPending   ReminderStatus = "pending"
	ReminderSent      ReminderStatus = "sent"
	ReminderDismissed ReminderStatus = "dismissed"
)

// IsValid reports whether the status is known.
func (s ReminderStatus) IsValid() bool {
	switch s {
	case ReminderPending, ReminderSent, ReminderDismissed:
		return true
	default:
		return false
	}
}

// Reminder is one planned notification about a task.
type Reminder struct {
	ID     int64
	TaskID int64
	FireAt time.Time
	Kind   ReminderKind
	Status ReminderStatus
	SentAt *time.Time
	// NotifiedAt records that the live event was published once. Publishing is
	// best effort: it does not mark the reminder as delivered, because nobody may
	// be listening. The reminder stays available for a client that reconnects.
	NotifiedAt *time.Time
	CreatedAt  time.Time
}

// Delivered reports whether the user has actually been shown this reminder.
func (r Reminder) Delivered() bool {
	return r.Status == ReminderSent && r.SentAt != nil
}

// MaxRemindersPerTask bounds how many notifications a single task may produce.
// Reminders should help, not create noise, so the policy never schedules more
// than this.
const MaxRemindersPerTask = 3

// OverdueFollowUp is how long after the deadline a task is raised again.
const OverdueFollowUp = 2 * time.Hour

// PlanReminders derives the reminder moments for a task.
//
// The schedule adapts to importance and to how long the work takes:
//
//   - a high-priority or long task gets a warning a day ahead so it can be
//     planned, a short warning before the deadline, and a follow-up after it;
//   - a medium task gets the short warning, the deadline itself and the
//     follow-up;
//   - a small low-priority task is reminded only at the deadline.
//
// Only future moments are returned: a reminder that already passed is not worth
// showing. Tasks without a deadline and tasks that are not open produce nothing.
func PlanReminders(value *task.Task, now time.Time) []Reminder {
	if value == nil || value.Status != task.StatusOpen {
		return nil
	}
	loc, err := value.Location()
	if err != nil {
		loc = time.UTC
	}
	reference := now.UTC()

	var due time.Time
	dateOnly := false
	switch {
	case value.DueAt != nil:
		due = value.DueAt.UTC()
	case value.DueDate != nil && !value.DueDate.IsZero():
		due = value.DueDate.Time(loc).Add(9 * time.Hour)
		dateOnly = true
	default:
		return nil
	}

	long := value.EstimateMinutes != nil && *value.EstimateMinutes >= 90
	important := value.Priority == task.PriorityHigh

	moments := make([]Reminder, 0, MaxRemindersPerTask)
	add := func(at time.Time, kind ReminderKind) {
		if at.IsZero() || !at.After(reference) || len(moments) >= MaxRemindersPerTask {
			return
		}
		for _, existing := range moments {
			if existing.FireAt.Equal(at.UTC()) {
				return
			}
		}
		moments = append(moments, Reminder{TaskID: value.ID, FireAt: at.UTC(), Kind: kind, Status: ReminderPending})
	}

	if dateOnly {
		if important || long {
			add(dayBefore(due, loc, 18), ReminderLead)
		}
		add(due, ReminderDue)
		if important || long || value.Priority == task.PriorityMedium {
			// A date-only deadline has no exact moment, so the follow-up is what
			// turns "today" into "you missed today".
			add(due.Add(27*time.Hour), ReminderOverdue)
		}
	} else {
		if important {
			add(due.Add(-24*time.Hour), ReminderLead)
		}
		if important || long {
			add(due.Add(-2*time.Hour), ReminderLead)
		}
		add(due, ReminderDue)
		if important || long || value.Priority == task.PriorityMedium {
			add(due.Add(OverdueFollowUp), ReminderOverdue)
		}
	}

	// Keep the schedule ordered: the earliest reminder fires first.
	for i := 1; i < len(moments); i++ {
		for j := i; j > 0 && moments[j].FireAt.Before(moments[j-1].FireAt); j-- {
			moments[j], moments[j-1] = moments[j-1], moments[j]
		}
	}
	return moments
}

// dayBefore returns the given hour on the calendar day before the reference
// instant, in the task's own timezone.
func dayBefore(reference time.Time, loc *time.Location, hour int) time.Time {
	local := reference.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, loc).AddDate(0, 0, -1)
}
