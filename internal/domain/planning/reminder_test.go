package planning

import (
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func newTask(id int64, priority task.Priority) *task.Task {
	return &task.Task{ID: id, Title: "задача", Priority: priority, Status: task.StatusOpen, Timezone: "Europe/Moscow"}
}

func TestPlanRemindersForExactDeadline(t *testing.T) {
	now := at(t, "2026-11-10T08:00:00Z")
	value := newTask(7, task.PriorityHigh)
	due := at(t, "2026-11-11T12:00:00Z")
	value.DueAt = &due

	reminders := PlanReminders(value, now)
	if len(reminders) != 3 {
		t.Fatalf("high-priority task got %d reminders, want 3: %+v", len(reminders), reminders)
	}
	want := []time.Time{
		due.Add(-24 * time.Hour),
		due.Add(-2 * time.Hour),
		due,
	}
	for i, expected := range want {
		if !reminders[i].FireAt.Equal(expected) {
			t.Errorf("reminder %d at %s, want %s", i, reminders[i].FireAt, expected)
		}
	}
	if reminders[0].Kind != ReminderLead || reminders[2].Kind != ReminderDue {
		t.Errorf("kinds = %v, %v", reminders[0].Kind, reminders[2].Kind)
	}
}

func TestPlanRemindersSkipsPastMoments(t *testing.T) {
	// Two hours before the deadline: the day-ahead warning is already in the past
	// and must not be replayed.
	now := at(t, "2026-11-11T11:00:00Z")
	value := newTask(8, task.PriorityHigh)
	due := at(t, "2026-11-11T12:00:00Z")
	value.DueAt = &due

	reminders := PlanReminders(value, now)
	for _, reminder := range reminders {
		if !reminder.FireAt.After(now) {
			t.Errorf("past reminder scheduled at %s (now %s)", reminder.FireAt, now)
		}
	}
	// The deadline and the follow-up are still ahead; only the lead warning that
	// already passed is dropped.
	if len(reminders) != 2 {
		t.Fatalf("reminders = %+v, want the deadline and the follow-up", reminders)
	}
	if reminders[0].Kind != ReminderDue || reminders[1].Kind != ReminderOverdue {
		t.Fatalf("kinds = %s, %s; want due then overdue", reminders[0].Kind, reminders[1].Kind)
	}
}

func TestPlanRemindersForDateOnlyDeadline(t *testing.T) {
	now := at(t, "2026-11-10T06:00:00Z") // 09:00 Moscow
	value := newTask(9, task.PriorityMedium)
	date, err := task.ParseCalendarDate("2026-11-12")
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	value.DueDate = &date

	reminders := PlanReminders(value, now)
	if len(reminders) != 2 {
		t.Fatalf("medium date-only task got %d reminders, want 2: %+v", len(reminders), reminders)
	}

	loc, _ := time.LoadLocation("Europe/Moscow")
	wantDue := time.Date(2026, 11, 12, 9, 0, 0, 0, loc)
	if !reminders[0].FireAt.Equal(wantDue.UTC()) {
		t.Errorf("first reminder at %s, want the morning of the deadline %s", reminders[0].FireAt, wantDue.UTC())
	}
	// Medium tasks also get a follow-up so a missed date does not disappear.
	if reminders[1].Kind != ReminderOverdue {
		t.Errorf("second reminder kind = %s, want overdue", reminders[1].Kind)
	}
}

func TestPlanRemindersWithoutDeadline(t *testing.T) {
	reminders := PlanReminders(newTask(10, task.PriorityHigh), at(t, "2026-11-10T08:00:00Z"))
	if len(reminders) != 0 {
		t.Fatalf("task without a deadline produced %d reminders", len(reminders))
	}
}

func TestPlanRemindersIgnoreFinishedTasks(t *testing.T) {
	value := newTask(11, task.PriorityHigh)
	due := at(t, "2026-11-11T12:00:00Z")
	value.DueAt = &due
	value.Status = task.StatusCompleted

	if reminders := PlanReminders(value, at(t, "2026-11-10T08:00:00Z")); len(reminders) != 0 {
		t.Fatalf("completed task produced %d reminders", len(reminders))
	}
}

func TestPlanRemindersAreCapped(t *testing.T) {
	value := newTask(12, task.PriorityHigh)
	estimate := 600
	value.EstimateMinutes = &estimate
	due := at(t, "2026-11-12T12:00:00Z")
	value.DueAt = &due

	reminders := PlanReminders(value, at(t, "2026-11-10T08:00:00Z"))
	if len(reminders) > MaxRemindersPerTask {
		t.Fatalf("%d reminders, cap is %d", len(reminders), MaxRemindersPerTask)
	}
}
