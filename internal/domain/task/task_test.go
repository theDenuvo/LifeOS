package task

import (
	"errors"
	"testing"
	"time"
)

func TestNewTask(t *testing.T) {
	tz := "Europe/Moscow"
	task, err := NewTask("Test Task", PriorityMedium, tz)
	if err != nil {
		t.Fatalf("NewTask failed: %v", err)
	}

	if task.Title != "Test Task" {
		t.Errorf("Title = %q, want \"Test Task\"", task.Title)
	}
	if task.Priority != PriorityMedium {
		t.Errorf("Priority = %v, want %v", task.Priority, PriorityMedium)
	}
	if task.Status != StatusOpen {
		t.Errorf("Status = %v, want %v", task.Status, StatusOpen)
	}
	if task.Timezone != tz {
		t.Errorf("Timezone = %q, want %q", task.Timezone, tz)
	}
	if task.ID != 0 {
		t.Errorf("ID should be 0 for new task, got %d", task.ID)
	}
	if task.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if task.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
	if task.Tags == nil {
		t.Error("Tags should be empty slice, not nil")
	}
}

func TestNewTask_InvalidTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
	}{
		{"empty", ""},
		{"too long", string(make([]byte, 501))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTask(tt.title, PriorityMedium, "Europe/Moscow")
			if err == nil {
				t.Errorf("expected error for title %q", tt.title)
			}
		})
	}
}

func TestNewTask_InvalidPriority(t *testing.T) {
	_, err := NewTask("Test", Priority("invalid"), "Europe/Moscow")
	if err == nil {
		t.Error("expected error for invalid priority")
	}
	if !errors.Is(err, ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestNewTask_InvalidTimezone(t *testing.T) {
	_, err := NewTask("Test", PriorityMedium, "Invalid/Timezone")
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
	if !errors.Is(err, ErrInvalidTimezone) {
		t.Errorf("expected ErrInvalidTimezone, got %v", err)
	}
}

func TestTask_Validate(t *testing.T) {
	tz := "Europe/Moscow"
	task, _ := NewTask("Valid Task", PriorityHigh, tz)

	if err := task.Validate(); err != nil {
		t.Errorf("valid task failed validation: %v", err)
	}

	// Test invalid title
	task.Title = ""
	if err := task.Validate(); err == nil {
		t.Error("expected error for empty title")
	}

	// Test invalid priority
	task.Title = "Valid"
	task.Priority = Priority("invalid")
	if err := task.Validate(); err == nil {
		t.Error("expected error for invalid priority")
	}

	// Test invalid status
	task.Priority = PriorityHigh
	task.Status = Status("invalid")
	if err := task.Validate(); err == nil {
		t.Error("expected error for invalid status")
	}

	// Test invalid timezone
	task.Status = StatusOpen
	task.Timezone = "Invalid/Zone"
	if err := task.Validate(); err == nil {
		t.Error("expected error for invalid timezone")
	}

	// Test due_at before created_at
	task.Timezone = tz
	dueAt := task.CreatedAt.Add(-time.Hour)
	task.DueAt = &dueAt
	err := task.Validate()
	if err == nil {
		t.Error("expected error for due_at before created_at")
	}
	if !errors.Is(err, ErrInvalidDueAtValue) {
		t.Errorf("expected ErrInvalidDueAtValue, got %v", err)
	}

	// Test invalid tag
	task.DueAt = nil
	task.Tags = []string{string(make([]byte, 51))}
	err = task.Validate()
	if err == nil {
		t.Error("expected error for long tag")
	}
	if !errors.Is(err, ErrInvalidTag) {
		t.Errorf("expected ErrInvalidTag, got %v", err)
	}
}

func TestCalendarDate_Parse(t *testing.T) {
	tests := []struct {
		input    string
		expected CalendarDate
		hasError bool
	}{
		{"2026-11-10", CalendarDate{Year: 2026, Month: 11, Day: 10}, false},
		{"2026-01-01", CalendarDate{Year: 2026, Month: 1, Day: 1}, false},
		{"2026-12-31", CalendarDate{Year: 2026, Month: 12, Day: 31}, false},
		{"invalid", CalendarDate{}, true},
		{"2026/11/10", CalendarDate{}, true},
		{"", CalendarDate{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			date, err := ParseCalendarDate(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("expected error for %q", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if date != tt.expected {
					t.Errorf("got %v, want %v", date, tt.expected)
				}
			}
		})
	}
}

func TestCalendarDate_String(t *testing.T) {
	date := CalendarDate{Year: 2026, Month: 11, Day: 10}
	if date.String() != "2026-11-10" {
		t.Errorf("String() = %q, want \"2026-11-10\"", date.String())
	}

	// Zero date
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

	date := CalendarDate{Year: 2026, Month: 1, Day: 1}
	if date.IsZero() {
		t.Error("non-zero date should not be IsZero()")
	}
}

func TestCalendarDate_Time(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Moscow")
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

	// Zero date
	var zero CalendarDate
	if !zero.Time(loc).IsZero() {
		t.Error("zero date Time() should be zero")
	}
}

func TestCalendarDate_EndOfDay(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Moscow")
	date := CalendarDate{Year: 2026, Month: 11, Day: 10}
	end := date.EndOfDay(loc)

	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Errorf("EndOfDay() = %v, want 23:59:59", end)
	}
	if end.Location() != loc {
		t.Error("EndOfDay() should be in the provided location")
	}

	// Zero date
	var zero CalendarDate
	if !zero.EndOfDay(loc).IsZero() {
		t.Error("zero date EndOfDay() should be zero")
	}
}

func TestTaskOptions(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")

	// Test WithNote
	err := task.Update(time.Now().UTC(), WithNote("A note"))
	if err != nil {
		t.Fatalf("WithNote failed: %v", err)
	}
	if task.Note == nil || *task.Note != "A note" {
		t.Error("Note not set correctly")
	}

	// Test WithPlannedDate
	planned := CalendarDate{Year: 2026, Month: 12, Day: 1}
	err = task.Update(time.Now().UTC(), WithPlannedDate(planned))
	if err != nil {
		t.Fatalf("WithPlannedDate failed: %v", err)
	}
	if task.PlannedDate == nil || *task.PlannedDate != planned {
		t.Error("PlannedDate not set correctly")
	}

	// Test WithDueDate
	due := CalendarDate{Year: 2026, Month: 12, Day: 15}
	err = task.Update(time.Now().UTC(), WithDueDate(due))
	if err != nil {
		t.Fatalf("WithDueDate failed: %v", err)
	}
	if task.DueDate == nil || *task.DueDate != due {
		t.Error("DueDate not set correctly")
	}

	// Test WithDueAt
	dueAt := time.Date(2026, 12, 15, 18, 0, 0, 0, time.UTC)
	err = task.Update(time.Now().UTC(), WithDueAt(dueAt))
	if err != nil {
		t.Fatalf("WithDueAt failed: %v", err)
	}
	if task.DueAt == nil || !task.DueAt.Equal(dueAt) {
		t.Error("DueAt not set correctly")
	}

	// Test WithTimezone
	err = task.Update(time.Now().UTC(), WithTimezone("America/New_York"))
	if err != nil {
		t.Fatalf("WithTimezone failed: %v", err)
	}
	if task.Timezone != "America/New_York" {
		t.Error("Timezone not set correctly")
	}

	// Test WithTags
	tags := []string{"tag1", "tag2"}
	err = task.Update(time.Now().UTC(), WithTags(tags))
	if err != nil {
		t.Fatalf("WithTags failed: %v", err)
	}
	if len(task.Tags) != 2 || task.Tags[0] != "tag1" {
		t.Error("Tags not set correctly")
	}

	// Test WithParentTaskID
	var parentID int64 = 42
	err = task.Update(time.Now().UTC(), WithParentTaskID(parentID))
	if err != nil {
		t.Fatalf("WithParentTaskID failed: %v", err)
	}
	if task.ParentTaskID == nil || *task.ParentTaskID != parentID {
		t.Error("ParentTaskID not set correctly")
	}

	// Test invalid tag length
	err = task.Update(time.Now().UTC(), WithTags([]string{string(make([]byte, 51))}))
	if err == nil {
		t.Error("expected error for long tag")
	}

	// Test invalid timezone
	err = task.Update(time.Now().UTC(), WithTimezone("Invalid/Zone"))
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
}

func TestTask_Complete(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	err := task.Complete(now)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if task.Status != StatusCompleted {
		t.Errorf("Status = %v, want %v", task.Status, StatusCompleted)
	}
	if task.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
	if !task.CompletedAt.Equal(now.UTC()) {
		t.Errorf("CompletedAt = %v, want %v", task.CompletedAt, now.UTC())
	}
	if !task.UpdatedAt.Equal(now.UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, now.UTC())
	}
	if task.CancelledAt != nil {
		t.Error("CancelledAt should be nil")
	}
}

func TestTask_Complete_InvalidTransition(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	task.Status = StatusCompleted
	now := time.Now().UTC()

	err := task.Complete(now)
	if err == nil {
		t.Error("expected error for invalid transition")
	}
	if !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Cancel(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	err := task.Cancel(now)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	if task.Status != StatusCancelled {
		t.Errorf("Status = %v, want %v", task.Status, StatusCancelled)
	}
	if task.CancelledAt == nil {
		t.Error("CancelledAt should be set")
	}
	if !task.CancelledAt.Equal(now.UTC()) {
		t.Errorf("CancelledAt = %v, want %v", task.CancelledAt, now.UTC())
	}
	if !task.UpdatedAt.Equal(now.UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, now.UTC())
	}
	if task.CompletedAt != nil {
		t.Error("CompletedAt should be nil")
	}
}

func TestTask_Cancel_InvalidTransition(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	task.Status = StatusCancelled
	now := time.Now().UTC()

	err := task.Cancel(now)
	if err == nil {
		t.Error("expected error for invalid transition")
	}
	if !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Restore(t *testing.T) {
	// Test restore from completed
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	task.Complete(now)

	restoreTime := now.Add(time.Hour)
	err := task.Restore(restoreTime)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	if task.Status != StatusOpen {
		t.Errorf("Status = %v, want %v", task.Status, StatusOpen)
	}
	if task.CompletedAt != nil {
		t.Error("CompletedAt should be nil after restore")
	}
	if task.CancelledAt != nil {
		t.Error("CancelledAt should be nil")
	}
	if !task.UpdatedAt.Equal(restoreTime.UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, restoreTime.UTC())
	}

	// Test restore from cancelled
	task2, _ := NewTask("Test2", PriorityMedium, "Europe/Moscow")
	task2.Cancel(now)
	err = task2.Restore(restoreTime)
	if err != nil {
		t.Fatalf("Restore from cancelled failed: %v", err)
	}
	if task2.Status != StatusOpen {
		t.Error("Status should be open after restore from cancelled")
	}
}

func TestTask_Restore_InvalidTransition(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Now().UTC()

	err := task.Restore(now)
	if err == nil {
		t.Error("expected error for invalid transition from open")
	}
	if !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Reschedule(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	planned := CalendarDate{Year: 2026, Month: 12, Day: 1}
	due := CalendarDate{Year: 2026, Month: 12, Day: 15}
	dueAt := time.Date(2026, 12, 15, 18, 0, 0, 0, time.UTC)

	err := task.Reschedule(now, WithPlannedDate(planned), WithDueDate(due), WithDueAt(dueAt))
	if err != nil {
		t.Fatalf("Reschedule failed: %v", err)
	}

	if task.PlannedDate == nil || *task.PlannedDate != planned {
		t.Error("PlannedDate not updated")
	}
	if task.DueDate == nil || *task.DueDate != due {
		t.Error("DueDate not updated")
	}
	if task.DueAt == nil || !task.DueAt.Equal(dueAt) {
		t.Error("DueAt not updated")
	}
	if !task.UpdatedAt.Equal(now.UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, now.UTC())
	}
}

func TestTask_Reschedule_InvalidTransition(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	task.Status = StatusCompleted
	now := time.Now().UTC()

	err := task.Reschedule(now)
	if err == nil {
		t.Error("expected error for invalid transition")
	}
	if !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Update(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	err := task.Update(now, WithNote("Updated note"), WithTags([]string{"tag1", "tag2"}))
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if task.Note == nil || *task.Note != "Updated note" {
		t.Error("Note not updated")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "tag1" {
		t.Error("Tags not updated")
	}
	if !task.UpdatedAt.Equal(now.UTC()) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, now.UTC())
	}
}

func TestTask_IsOverdue_DueAt(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	dueAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	task.DueAt = &dueAt

	tests := []struct {
		name     string
		now      time.Time
		expected bool
	}{
		{"before due", dueAt.Add(-time.Hour), false},
		{"at due", dueAt, false}, // not overdue until after
		{"after due", dueAt.Add(time.Second), true},
		{"well after due", dueAt.Add(time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.IsOverdue(tt.now); got != tt.expected {
				t.Errorf("IsOverdue(%v) = %v, want %v", tt.now, got, tt.expected)
			}
		})
	}
}

func TestTask_IsOverdue_DueDate(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	task.DueDate = &dueDate

	loc, _ := time.LoadLocation("Europe/Moscow")
	endOfDay := time.Date(2026, 11, 10, 23, 59, 59, 0, loc)

	tests := []struct {
		name     string
		now      time.Time
		expected bool
	}{
		{"before end of day", endOfDay.Add(-time.Hour), false},
		{"at end of day", endOfDay, false},
		{"after end of day", endOfDay.Add(time.Second), true},
		{"next day", endOfDay.Add(24 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.IsOverdue(tt.now); got != tt.expected {
				t.Errorf("IsOverdue(%v) = %v, want %v", tt.now, got, tt.expected)
			}
		})
	}
}

func TestTask_IsOverdue_DueAtTakesPriority(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	// dueAt is in the future (Nov 12), dueDate is in the past (Nov 10)
	dueAt := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	task.DueAt = &dueAt
	task.DueDate = &dueDate

	// DueAt should take priority - if dueAt is in future but dueDate is past, not overdue
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	if task.IsOverdue(now) {
		t.Error("task should not be overdue when dueAt is in future, even if dueDate is past")
	}

	// If dueAt is past, overdue regardless of dueDate
	now = time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)
	if !task.IsOverdue(now) {
		t.Error("task should be overdue when dueAt is past")
	}
}

func TestTask_IsOverdue_NonOpenStatus(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	dueAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	task.DueAt = &dueAt

	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{"open", StatusOpen, true},
		{"completed", StatusCompleted, false},
		{"cancelled", StatusCancelled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task.Status = tt.status
			if got := task.IsOverdue(now); got != tt.expected {
				t.Errorf("IsOverdue with status %v = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestTask_IsOverdue_NoDeadline(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	now := time.Now().UTC()

	if task.IsOverdue(now) {
		t.Error("task with no deadline should not be overdue")
	}
}

func TestTask_OverdueAt(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")

	// Test with dueAt
	dueAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	task.DueAt = &dueAt
	overdue := task.OverdueAt()
	if overdue == nil || !overdue.Equal(dueAt) {
		t.Errorf("OverdueAt with dueAt = %v, want %v", overdue, dueAt)
	}

	// Test with dueDate only
	task.DueAt = nil
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	task.DueDate = &dueDate
	loc, _ := time.LoadLocation("Europe/Moscow")
	expectedEndOfDay := time.Date(2026, 11, 10, 23, 59, 59, 0, loc)
	overdue = task.OverdueAt()
	if overdue == nil || !overdue.Equal(expectedEndOfDay) {
		t.Errorf("OverdueAt with dueDate = %v, want %v", overdue, expectedEndOfDay)
	}

	// Test with no deadline
	task.DueDate = nil
	if task.OverdueAt() != nil {
		t.Error("OverdueAt should be nil when no deadline")
	}
}

func TestTask_Location(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	loc, err := task.Location()
	if err != nil {
		t.Fatalf("Location() failed: %v", err)
	}
	if loc.String() != "Europe/Moscow" {
		t.Errorf("Location = %q, want \"Europe/Moscow\"", loc.String())
	}

	// Test with different timezone
	task.Timezone = "America/New_York"
	loc, err = task.Location()
	if err != nil {
		t.Fatalf("Location() failed: %v", err)
	}
	if loc.String() != "America/New_York" {
		t.Errorf("Location = %q, want \"America/New_York\"", loc.String())
	}
}

func TestTask_ToEvent(t *testing.T) {
	task, _ := NewTask("Test", PriorityMedium, "Europe/Moscow")
	task.ID = 42

	event, err := task.ToEvent(EventTypeCreated, map[string]string{"source": "user"}, ActorUser)
	if err != nil {
		t.Fatalf("ToEvent failed: %v", err)
	}

	if event.TaskID != 42 {
		t.Errorf("TaskID = %d, want 42", event.TaskID)
	}
	if event.Type != EventTypeCreated {
		t.Errorf("Type = %v, want %v", event.Type, EventTypeCreated)
	}
	if event.Actor != ActorUser {
		t.Errorf("Actor = %v, want %v", event.Actor, ActorUser)
	}

	var payload map[string]string
	if err := event.PayloadAs(&payload); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if payload["source"] != "user" {
		t.Errorf("payload source = %q, want \"user\"", payload["source"])
	}
}
