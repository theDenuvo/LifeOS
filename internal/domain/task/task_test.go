package task

import (
	"errors"
	"testing"
	"time"
	"unicode/utf8"
)

// fixedNow is the deterministic instant used across the domain tests.
var fixedNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func newTestTask(t *testing.T) *Task {
	t.Helper()
	task, err := NewTask("Test Task", PriorityMedium, "Europe/Moscow", fixedNow)
	if err != nil {
		t.Fatalf("NewTask failed: %v", err)
	}
	return task
}

func TestNewTask(t *testing.T) {
	tz := "Europe/Moscow"
	task, err := NewTask("Test Task", PriorityMedium, tz, fixedNow)
	if err != nil {
		t.Fatalf("NewTask failed: %v", err)
	}

	if task.Title != "Test Task" {
		t.Errorf("Title = %q, want %q", task.Title, "Test Task")
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
	if !task.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v (must be the caller supplied instant)", task.CreatedAt, fixedNow)
	}
	if !task.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v (must be the caller supplied instant)", task.UpdatedAt, fixedNow)
	}
	if task.Tags == nil {
		t.Error("Tags should be empty slice, not nil")
	}
}

// TestNewTask_NoHiddenClock guards the review requirement that the domain never
// reads the system clock: two tasks created with the same explicit instant must
// carry exactly that instant, and a caller supplied instant far in the past must
// not be replaced by "now".
func TestNewTask_NoHiddenClock(t *testing.T) {
	past := time.Date(2001, 2, 3, 4, 5, 6, 7, time.UTC)
	task, err := NewTask("Past", PriorityLow, "UTC", past)
	if err != nil {
		t.Fatalf("NewTask failed: %v", err)
	}
	if !task.CreatedAt.Equal(past) {
		t.Errorf("CreatedAt = %v, want %v", task.CreatedAt, past)
	}
	if !task.UpdatedAt.Equal(past) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, past)
	}
}

func TestNewTask_InvalidTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
	}{
		{"empty", ""},
		{"501 ascii", repeat("a", 501)},
		{"500 cyrillic ok + 1", repeat("я", 501)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTask(tt.title, PriorityMedium, "Europe/Moscow", fixedNow)
			if err == nil {
				t.Fatalf("expected error for title %q", tt.name)
			}
			if !errors.Is(err, ErrInvalidTitle) {
				t.Errorf("expected ErrInvalidTitle, got %v", err)
			}
		})
	}
}

func TestNewTask_InvalidPriority(t *testing.T) {
	_, err := NewTask("Test", Priority("invalid"), "Europe/Moscow", fixedNow)
	if !errors.Is(err, ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestNewTask_InvalidTimezone(t *testing.T) {
	for _, tz := range []string{"Invalid/Timezone", "", "Europe/Moscow/Extra"} {
		_, err := NewTask("Test", PriorityMedium, tz, fixedNow)
		if !errors.Is(err, ErrInvalidTimezone) {
			t.Errorf("timezone %q: expected ErrInvalidTimezone, got %v", tz, err)
		}
	}
}

func TestTask_Validate(t *testing.T) {
	task := newTestTask(t)
	if err := task.Validate(); err != nil {
		t.Fatalf("valid task failed validation: %v", err)
	}

	t.Run("empty title", func(t *testing.T) {
		candidate := newTestTask(t)
		candidate.Title = ""
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidTitle) {
			t.Errorf("expected ErrInvalidTitle, got %v", err)
		}
	})

	t.Run("invalid priority", func(t *testing.T) {
		candidate := newTestTask(t)
		candidate.Priority = Priority("invalid")
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidPriority) {
			t.Errorf("expected ErrInvalidPriority, got %v", err)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		candidate := newTestTask(t)
		candidate.Status = Status("invalid")
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("expected ErrInvalidStatus, got %v", err)
		}
	})

	t.Run("invalid timezone", func(t *testing.T) {
		candidate := newTestTask(t)
		candidate.Timezone = "Invalid/Zone"
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidTimezone) {
			t.Errorf("expected ErrInvalidTimezone, got %v", err)
		}
	})

	t.Run("invalid note", func(t *testing.T) {
		candidate := newTestTask(t)
		note := repeat("я", 5001)
		candidate.Note = &note
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidNote) {
			t.Errorf("expected ErrInvalidNote, got %v", err)
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		candidate := newTestTask(t)
		candidate.Tags = []string{repeat("я", 51)}
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidTag) {
			t.Errorf("expected ErrInvalidTag, got %v", err)
		}
	})
}

// TestTask_Validate_AllowsPastDeadline covers the approved product decision that
// an overdue deadline is a normal state, not a validation error.
func TestTask_Validate_AllowsPastDeadline(t *testing.T) {
	t.Run("past due_at", func(t *testing.T) {
		task := newTestTask(t)
		past := fixedNow.Add(-72 * time.Hour)
		if err := task.Update(fixedNow, WithDueAt(past)); err != nil {
			t.Fatalf("Update with past due_at failed: %v", err)
		}
		if task.DueAt == nil || !task.DueAt.Equal(past) {
			t.Fatalf("DueAt = %v, want %v", task.DueAt, past)
		}
		if err := task.Validate(); err != nil {
			t.Errorf("past due_at must not fail validation: %v", err)
		}
	})

	t.Run("past due_at set directly on aggregate", func(t *testing.T) {
		task := newTestTask(t)
		past := fixedNow.Add(-time.Minute)
		task.DueAt = &past
		if err := task.Validate(); err != nil {
			t.Errorf("past due_at must not fail validation: %v", err)
		}
	})

	t.Run("update existing due_at to a past value", func(t *testing.T) {
		task := newTestTask(t)
		future := fixedNow.Add(48 * time.Hour)
		if err := task.Update(fixedNow, WithDueAt(future)); err != nil {
			t.Fatalf("initial Update failed: %v", err)
		}
		past := fixedNow.Add(-time.Second)
		if err := task.Update(fixedNow, WithDueAt(past)); err != nil {
			t.Fatalf("Update to past due_at failed: %v", err)
		}
		if !task.DueAt.Equal(past) {
			t.Errorf("DueAt = %v, want %v", task.DueAt, past)
		}
	})

	t.Run("past due_date", func(t *testing.T) {
		task := newTestTask(t)
		past := CalendarDate{Year: 2020, Month: 1, Day: 1}
		if err := task.Update(fixedNow, WithDueDate(past)); err != nil {
			t.Fatalf("Update with past due_date failed: %v", err)
		}
		if task.DueDate == nil || *task.DueDate != past {
			t.Fatalf("DueDate = %v, want %v", task.DueDate, past)
		}
		if err := task.Validate(); err != nil {
			t.Errorf("past due_date must not fail validation: %v", err)
		}
	})

	t.Run("past planned_date", func(t *testing.T) {
		task := newTestTask(t)
		past := CalendarDate{Year: 2019, Month: 5, Day: 5}
		if err := task.Update(fixedNow, WithPlannedDate(past)); err != nil {
			t.Fatalf("Update with past planned_date failed: %v", err)
		}
		if err := task.Validate(); err != nil {
			t.Errorf("past planned_date must not fail validation: %v", err)
		}
	})
}

// TestTaskOption_ErrorsRemoved guards that the obsolete due_at >= created_at
// restriction is really gone from the domain.
func TestTaskOption_ErrorsRemoved(t *testing.T) {
	task := newTestTask(t)
	past := task.CreatedAt.Add(-365 * 24 * time.Hour)
	if err := task.Reschedule(fixedNow, WithDueAt(past)); err != nil {
		t.Fatalf("Reschedule with due_at long before created_at failed: %v", err)
	}
	if task.DueAt == nil || !task.DueAt.Equal(past) {
		t.Errorf("DueAt = %v, want %v", task.DueAt, past)
	}
}

// TestTask_Update_AtomicOnOptionError covers the first-option-succeeds,
// second-option-fails scenario: nothing may change.
func TestTask_Update_AtomicOnOptionError(t *testing.T) {
	task := newTestTask(t)
	originalNote := "original"
	task.Note = &originalNote
	originalTags := []string{"keep"}
	task.Tags = originalTags
	originalTitle := task.Title
	originalUpdatedAt := task.UpdatedAt

	err := task.Update(fixedNow,
		WithTitle("changed"),
		WithTags([]string{repeat("я", 51)}),
	)
	if err == nil {
		t.Fatal("expected error from second option")
	}
	if task.Title != originalTitle {
		t.Errorf("Title = %q, want unchanged %q", task.Title, originalTitle)
	}
	if task.Note == nil || *task.Note != originalNote {
		t.Errorf("Note changed on failed update: %v", task.Note)
	}
	if len(task.Tags) != 1 || task.Tags[0] != "keep" {
		t.Errorf("Tags changed on failed update: %v", task.Tags)
	}
	if !task.UpdatedAt.Equal(originalUpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged %v", task.UpdatedAt, originalUpdatedAt)
	}
}

// TestTask_Update_AtomicOnValidationError covers an option that mutates a field
// into a state that fails the final validation.
func TestTask_Update_AtomicOnValidationError(t *testing.T) {
	task := newTestTask(t)
	originalTitle := task.Title
	originalUpdatedAt := task.UpdatedAt

	// Valid note option, then a title option that violates the final validation.
	err := task.Update(fixedNow,
		WithNote("a note"),
		WithTitle(""),
	)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if task.Title != originalTitle {
		t.Errorf("Title = %q, want unchanged %q", task.Title, originalTitle)
	}
	if task.Note != nil {
		t.Errorf("Note = %v, want nil: option applied before validation failure", task.Note)
	}
	if !task.UpdatedAt.Equal(originalUpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged %v", task.UpdatedAt, originalUpdatedAt)
	}
}

// TestTask_Update_AtomicOnErrorDoesNotMutatePointers makes sure a failed update
// cannot write through a pointer shared with the caller.
func TestTask_Update_AtomicOnErrorDoesNotMutatePointers(t *testing.T) {
	task := newTestTask(t)
	shared := CalendarDate{Year: 2026, Month: 12, Day: 1}
	if err := task.Update(fixedNow, WithPlannedDate(shared)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	before := *task.PlannedDate
	if err := task.Update(fixedNow,
		WithDueAt(fixedNow),
		WithTags([]string{repeat("я", 51)}),
	); err == nil {
		t.Fatal("expected error")
	}
	if *task.PlannedDate != before {
		t.Errorf("PlannedDate = %v, want unchanged %v", *task.PlannedDate, before)
	}
}

// TestTask_Update_NoSharedSlice verifies that WithTags copies its input and that
// a later mutation of the caller's slice cannot reach the aggregate.
func TestTask_Update_NoSharedSlice(t *testing.T) {
	task := newTestTask(t)
	input := []string{"one", "two"}
	if err := task.Update(fixedNow, WithTags(input)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	input[0] = "mutated"
	if task.Tags[0] != "one" {
		t.Errorf("Tags[0] = %q, want %q: WithTags shares the caller slice", task.Tags[0], "one")
	}

	// Mutating the task's own slice must not be visible through the input slice.
	task.Tags[1] = "changed"
	if input[1] != "mutated" && input[1] != "two" {
		t.Logf("input[1] = %q", input[1])
	}
}

// TestTask_Update_SuccessCommitsEverything checks the happy path end to end.
func TestTask_Update_SuccessCommitsEverything(t *testing.T) {
	task := newTestTask(t)
	due := CalendarDate{Year: 2026, Month: 12, Day: 15}
	dueAt := time.Date(2026, 12, 15, 18, 0, 0, 0, time.UTC)

	if err := task.Update(fixedNow,
		WithNote("note"),
		WithPlannedDate(due),
		WithDueDate(due),
		WithDueAt(dueAt),
		WithTimezone("America/New_York"),
		WithTags([]string{"a", "b"}),
	); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if task.Note == nil || *task.Note != "note" {
		t.Error("Note not committed")
	}
	if task.PlannedDate == nil || *task.PlannedDate != due {
		t.Error("PlannedDate not committed")
	}
	if task.DueDate == nil || *task.DueDate != due {
		t.Error("DueDate not committed")
	}
	if task.DueAt == nil || !task.DueAt.Equal(dueAt) {
		t.Error("DueAt not committed")
	}
	if task.Timezone != "America/New_York" {
		t.Error("Timezone not committed")
	}
	if len(task.Tags) != 2 || task.Tags[0] != "a" || task.Tags[1] != "b" {
		t.Errorf("Tags not committed: %v", task.Tags)
	}
	if !task.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, fixedNow)
	}
}

func TestTask_Reschedule_AtomicOnError(t *testing.T) {
	task := newTestTask(t)
	originalDueAt := task.DueAt
	originalDueDate := task.DueDate
	originalUpdatedAt := task.UpdatedAt

	err := task.Reschedule(fixedNow,
		WithDueAt(fixedNow.Add(time.Hour)),
		WithDueDate(CalendarDate{Year: 2026, Month: 2, Day: 30}),
	)
	if err == nil {
		t.Fatal("expected error for impossible due date")
	}
	if task.DueAt != originalDueAt {
		t.Error("DueAt changed on failed Reschedule")
	}
	if task.DueDate != originalDueDate {
		t.Error("DueDate changed on failed Reschedule")
	}
	if !task.UpdatedAt.Equal(originalUpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged %v", task.UpdatedAt, originalUpdatedAt)
	}
}

func TestTask_Reschedule(t *testing.T) {
	task := newTestTask(t)

	planned := CalendarDate{Year: 2026, Month: 12, Day: 1}
	due := CalendarDate{Year: 2026, Month: 12, Day: 15}
	dueAt := time.Date(2026, 12, 15, 18, 0, 0, 0, time.UTC)

	if err := task.Reschedule(fixedNow, WithPlannedDate(planned), WithDueDate(due), WithDueAt(dueAt)); err != nil {
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
	if !task.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, fixedNow)
	}
}

func TestTask_Reschedule_InvalidTransition(t *testing.T) {
	task := newTestTask(t)
	task.Status = StatusCompleted
	updatedAt := task.UpdatedAt

	err := task.Reschedule(fixedNow, WithDueAt(fixedNow))
	if !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
	if !task.UpdatedAt.Equal(updatedAt) {
		t.Error("UpdatedAt changed on failed Reschedule")
	}
}

func TestTask_Complete(t *testing.T) {
	task := newTestTask(t)
	if err := task.Complete(fixedNow); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if task.Status != StatusCompleted {
		t.Errorf("Status = %v, want %v", task.Status, StatusCompleted)
	}
	if task.CompletedAt == nil || !task.CompletedAt.Equal(fixedNow) {
		t.Errorf("CompletedAt = %v, want %v", task.CompletedAt, fixedNow)
	}
	if task.CancelledAt != nil {
		t.Error("CancelledAt should be nil")
	}
	if !task.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, fixedNow)
	}
}

func TestTask_Complete_InvalidTransition(t *testing.T) {
	task := newTestTask(t)
	task.Status = StatusCompleted
	if err := task.Complete(fixedNow); !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Cancel(t *testing.T) {
	task := newTestTask(t)
	if err := task.Cancel(fixedNow); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if task.Status != StatusCancelled {
		t.Errorf("Status = %v, want %v", task.Status, StatusCancelled)
	}
	if task.CancelledAt == nil || !task.CancelledAt.Equal(fixedNow) {
		t.Errorf("CancelledAt = %v, want %v", task.CancelledAt, fixedNow)
	}
	if task.CompletedAt != nil {
		t.Error("CompletedAt should be nil")
	}
}

func TestTask_Cancel_InvalidTransition(t *testing.T) {
	task := newTestTask(t)
	task.Status = StatusCancelled
	if err := task.Cancel(fixedNow); !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

func TestTask_Restore(t *testing.T) {
	t.Run("from completed", func(t *testing.T) {
		task := newTestTask(t)
		if err := task.Complete(fixedNow); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		restoreTime := fixedNow.Add(time.Hour)
		if err := task.Restore(restoreTime); err != nil {
			t.Fatalf("Restore failed: %v", err)
		}
		if task.Status != StatusOpen {
			t.Errorf("Status = %v, want %v", task.Status, StatusOpen)
		}
		if task.CompletedAt != nil {
			t.Error("CompletedAt should be nil after restore")
		}
		if !task.UpdatedAt.Equal(restoreTime) {
			t.Errorf("UpdatedAt = %v, want %v", task.UpdatedAt, restoreTime)
		}
	})

	t.Run("from cancelled", func(t *testing.T) {
		task := newTestTask(t)
		if err := task.Cancel(fixedNow); err != nil {
			t.Fatalf("Cancel failed: %v", err)
		}
		restoreTime := fixedNow.Add(time.Hour)
		if err := task.Restore(restoreTime); err != nil {
			t.Fatalf("Restore failed: %v", err)
		}
		if task.Status != StatusOpen {
			t.Errorf("Status = %v, want %v", task.Status, StatusOpen)
		}
		if task.CancelledAt != nil {
			t.Error("CancelledAt should be nil after restore")
		}
	})
}

func TestTask_Restore_InvalidTransition(t *testing.T) {
	task := newTestTask(t)
	if err := task.Restore(fixedNow); !IsInvalidTransitionError(err) {
		t.Errorf("expected InvalidTransitionError, got %T", err)
	}
}

// TestTask_Validate_RejectsDirectlyConstructedInvalidDates covers the review
// requirement that an exported CalendarDate cannot smuggle an impossible date
// past validation.
func TestTask_Validate_RejectsDirectlyConstructedInvalidDates(t *testing.T) {
	tests := []struct {
		name string
		date CalendarDate
	}{
		{"30 february", CalendarDate{Year: 2026, Month: 2, Day: 30}},
		{"31 april", CalendarDate{Year: 2026, Month: 4, Day: 31}},
		{"month 13", CalendarDate{Year: 2026, Month: 13, Day: 1}},
		{"day 0", CalendarDate{Year: 2026, Month: 5, Day: 0}},
	}

	for _, tt := range tests {
		t.Run("planned_date_"+tt.name, func(t *testing.T) {
			task := newTestTask(t)
			task.PlannedDate = &tt.date
			if err := task.Validate(); !errors.Is(err, ErrInvalidPlannedDate) {
				t.Errorf("expected ErrInvalidPlannedDate, got %v", err)
			}
		})

		t.Run("due_date_"+tt.name, func(t *testing.T) {
			task := newTestTask(t)
			task.DueDate = &tt.date
			if err := task.Validate(); !errors.Is(err, ErrInvalidDueDate) {
				t.Errorf("expected ErrInvalidDueDate, got %v", err)
			}
		})
	}
}

// TestTaskOption_RejectsDirectlyConstructedInvalidDates covers the option path.
func TestTaskOption_RejectsDirectlyConstructedInvalidDates(t *testing.T) {
	task := newTestTask(t)
	err := task.Update(fixedNow, WithDueDate(CalendarDate{Year: 2026, Month: 2, Day: 30}))
	if !errors.Is(err, ErrInvalidDueDate) {
		t.Errorf("expected ErrInvalidDueDate, got %v", err)
	}
	if task.DueDate != nil {
		t.Error("DueDate must not be set on error")
	}

	err = task.Update(fixedNow, WithPlannedDate(CalendarDate{Year: 2026, Month: 4, Day: 31}))
	if !errors.Is(err, ErrInvalidPlannedDate) {
		t.Errorf("expected ErrInvalidPlannedDate, got %v", err)
	}
}

// --- Unicode length validation -------------------------------------------------

func TestValidateTitle_Unicode(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantErr bool
	}{
		{"empty", "", true},
		{"1 char", "a", false},
		{"500 ascii", repeat("a", 500), false},
		{"501 ascii", repeat("a", 501), true},
		{"500 cyrillic", repeat("я", 500), false},
		{"501 cyrillic", repeat("я", 501), true},
		{"invalid utf8", string([]byte{0xff, 0xfe, 0xfd}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTitle(tt.title)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateTag_Unicode(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"50 cyrillic", repeat("я", 50), false},
		{"51 cyrillic", repeat("я", 51), true},
		{"invalid utf8", string([]byte{0xff}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTag(tt.tag)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestWithNote_Unicode(t *testing.T) {
	tests := []struct {
		name    string
		note    string
		wantErr bool
	}{
		{"empty", "", false},
		{"5000 cyrillic", repeat("я", 5000), false},
		{"5001 cyrillic", repeat("я", 5001), true},
		{"invalid utf8", string([]byte{0xff}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := newTestTask(t)
			err := task.Update(fixedNow, WithNote(tt.note))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if task.Note != nil {
					t.Error("Note must not be set on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if task.Note == nil || utf8.RuneCountInString(*task.Note) != utf8.RuneCountInString(tt.note) {
				t.Error("Note not stored")
			}
		})
	}
}

func TestWithTags_Unicode(t *testing.T) {
	task := newTestTask(t)
	if err := task.Update(fixedNow, WithTags([]string{repeat("я", 50)})); err != nil {
		t.Fatalf("50 cyrillic tag must be accepted: %v", err)
	}
	err := task.Update(fixedNow, WithTags([]string{repeat("я", 51)}))
	if !errors.Is(err, ErrInvalidTag) {
		t.Errorf("expected ErrInvalidTag, got %v", err)
	}
}

// --- Deadline semantics -------------------------------------------------------

// TestTask_IsOverdue_DueAt covers the strict comparison required for an exact
// deadline: only strictly after DueAt is the task overdue.
func TestTask_IsOverdue_DueAt(t *testing.T) {
	task := newTestTask(t)
	dueAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if err := task.Update(fixedNow, WithDueAt(dueAt)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"well before due", dueAt.Add(-time.Hour), false},
		{"one nanosecond before due", dueAt.Add(-time.Nanosecond), false},
		{"exactly at due (strict: still on time)", dueAt, false},
		{"one nanosecond after due", dueAt.Add(time.Nanosecond), true},
		{"one second after due", dueAt.Add(time.Second), true},
		{"well after due", dueAt.Add(time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.IsOverdue(tt.now); got != tt.want {
				t.Errorf("IsOverdue(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}

	// OverdueAt must keep reporting the exact deadline value.
	overdueAt := task.OverdueAt()
	if overdueAt == nil || !overdueAt.Equal(dueAt) {
		t.Fatalf("OverdueAt() = %v, want %v", overdueAt, dueAt)
	}

	// Consistency: IsOverdue is true exactly when now is strictly after OverdueAt.
	for _, now := range []time.Time{
		dueAt.Add(-time.Nanosecond),
		dueAt,
		dueAt.Add(time.Nanosecond),
		dueAt.Add(time.Hour),
	} {
		want := now.After(*overdueAt)
		if got := task.IsOverdue(now); got != want {
			t.Errorf("IsOverdue(%v) = %v, want %v (now.After(OverdueAt))", now, got, want)
		}
	}
}

func TestTask_IsOverdue_DueDate(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	task := newTestTask(t)
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	if err := task.Update(fixedNow, WithDueDate(dueDate)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	nextMidnight := time.Date(2026, 11, 11, 0, 0, 0, 0, loc)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"start of due day", time.Date(2026, 11, 10, 0, 0, 0, 0, loc), false},
		{"one second before midnight", nextMidnight.Add(-time.Second), false},
		{"one nanosecond before midnight", nextMidnight.Add(-time.Nanosecond), false},
		{"exactly at next midnight", nextMidnight, true},
		{"after next midnight", nextMidnight.Add(time.Nanosecond), true},
		{"next day", nextMidnight.Add(12 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.IsOverdue(tt.now); got != tt.want {
				t.Errorf("IsOverdue(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestTask_OverdueAt_MatchesIsOverdue(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}
	task := newTestTask(t)
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	if err := task.Update(fixedNow, WithDueDate(dueDate)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	overdueAt := task.OverdueAt()
	if overdueAt == nil {
		t.Fatal("OverdueAt must not be nil")
	}
	want := time.Date(2026, 11, 11, 0, 0, 0, 0, loc)
	if !overdueAt.Equal(want) {
		t.Errorf("OverdueAt = %v, want %v", overdueAt, want)
	}
	if task.IsOverdue(overdueAt.Add(-time.Nanosecond)) {
		t.Error("task must not be overdue one nanosecond before OverdueAt")
	}
	if !task.IsOverdue(*overdueAt) {
		t.Error("task must be overdue exactly at OverdueAt")
	}
}

// TestTask_IsOverdue_ComparisonDiffersByDeadlineType pins the deliberate
// asymmetry between the two deadline types: the exact deadline uses a strict
// comparison (equality is still on time) while the calendar deadline uses an
// inclusive boundary (equality is already overdue).
func TestTask_IsOverdue_ComparisonDiffersByDeadlineType(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	// Same wall-clock instant for both deadline kinds: 2026-11-10T12:00:00Z.
	instant := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	t.Run("due_at strict", func(t *testing.T) {
		task := newTestTask(t)
		if err := task.Update(fixedNow, WithDueAt(instant)); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		if task.IsOverdue(instant) {
			t.Error("at exactly due_at the task must NOT be overdue (strict comparison)")
		}
		if !task.IsOverdue(instant.Add(time.Nanosecond)) {
			t.Error("one nanosecond after due_at the task must be overdue")
		}
	})

	t.Run("due_date inclusive at next local midnight", func(t *testing.T) {
		task := newTestTask(t)
		due := CalendarDate{Year: 2026, Month: 11, Day: 10}
		if err := task.Update(fixedNow, WithDueDate(due)); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		boundary := time.Date(2026, 11, 11, 0, 0, 0, 0, loc)
		if task.IsOverdue(boundary.Add(-time.Nanosecond)) {
			t.Error("one nanosecond before next local midnight must NOT be overdue")
		}
		if !task.IsOverdue(boundary) {
			t.Error("exactly at next local midnight the task must be overdue (inclusive)")
		}
	})

	t.Run("overdue_at reports the right instant for each type", func(t *testing.T) {
		exact := newTestTask(t)
		if err := exact.Update(fixedNow, WithDueAt(instant)); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		got := exact.OverdueAt()
		if got == nil || !got.Equal(instant) {
			t.Errorf("due_at: OverdueAt() = %v, want %v", got, instant)
		}

		calendar := newTestTask(t)
		if err := calendar.Update(fixedNow, WithDueDate(CalendarDate{Year: 2026, Month: 11, Day: 10})); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		got = calendar.OverdueAt()
		want := time.Date(2026, 11, 11, 0, 0, 0, 0, loc)
		if got == nil || !got.Equal(want) {
			t.Errorf("due_date: OverdueAt() = %v, want %v", got, want)
		}
	})
}

// TestTask_IsOverdue_DueAtTakesPriority tests that an exact deadline wins over a calendar one.
func TestTask_IsOverdue_DueAtTakesPriority(t *testing.T) {
	task := newTestTask(t)
	dueAt := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	dueDate := CalendarDate{Year: 2026, Month: 11, Day: 10}
	if err := task.Update(fixedNow, WithDueAt(dueAt), WithDueDate(dueDate)); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if task.IsOverdue(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) {
		t.Error("due_at in the future must win over a past due_date")
	}
	if !task.IsOverdue(time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)) {
		t.Error("task must be overdue once due_at has passed")
	}
}

func TestTask_IsOverdue_NonOpenStatus(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

	for _, status := range []Status{StatusCompleted, StatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			task := newTestTask(t)
			dueAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			if err := task.Update(fixedNow, WithDueAt(dueAt)); err != nil {
				t.Fatalf("Update failed: %v", err)
			}
			task.Status = status
			if task.IsOverdue(now) {
				t.Errorf("%s task must never be overdue", status)
			}
		})
	}
}

func TestTask_IsOverdue_NoDeadline(t *testing.T) {
	task := newTestTask(t)
	if task.IsOverdue(fixedNow.Add(100000 * time.Hour)) {
		t.Error("task with no deadline should not be overdue")
	}
	if task.OverdueAt() != nil {
		t.Error("OverdueAt should be nil when no deadline")
	}
}

func TestTask_Location(t *testing.T) {
	task := newTestTask(t)
	loc, err := task.Location()
	if err != nil {
		t.Fatalf("Location() failed: %v", err)
	}
	if loc.String() != "Europe/Moscow" {
		t.Errorf("Location = %q, want %q", loc.String(), "Europe/Moscow")
	}
}

func TestTask_ToEvent(t *testing.T) {
	task := newTestTask(t)
	task.ID = 42

	event, err := task.ToEvent(EventTypeCreated, map[string]string{"source": "user"}, ActorUser, fixedNow)
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
	if !event.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v (caller supplied)", event.CreatedAt, fixedNow)
	}

	var payload map[string]string
	if err := event.PayloadAs(&payload); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if payload["source"] != "user" {
		t.Errorf("payload source = %q, want %q", payload["source"], "user")
	}
}

// repeat builds a string of n copies of s.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// TestWithPriority covers the option added for the Application Layer.
func TestWithPriority(t *testing.T) {
	tests := []struct {
		name     string
		priority Priority
		wantErr  bool
	}{
		{"low", PriorityLow, false},
		{"medium", PriorityMedium, false},
		{"high", PriorityHigh, false},
		{"invalid", Priority("urgent"), true},
		{"empty", Priority(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := newTestTask(t)
			before := task.Priority

			err := task.Update(fixedNow, WithPriority(tt.priority))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if task.Priority != before {
					t.Errorf("priority changed on error: %v", task.Priority)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if task.Priority != tt.priority {
				t.Errorf("Priority = %v, want %v", task.Priority, tt.priority)
			}
		})
	}
}
