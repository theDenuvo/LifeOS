package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// --- UpdateTask ---------------------------------------------------------------

func TestUpdateTask_PresentAndAbsentFields(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "Исходный заголовок",
		Note:  Some("исходная заметка"),
		Tags:  []string{"один"},
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title: Some("Новый заголовок"),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	// Supplied field changed...
	if updated.Task.Title != "Новый заголовок" {
		t.Errorf("Title = %q", updated.Task.Title)
	}
	// ...absent fields untouched.
	if updated.Task.Note == nil || *updated.Task.Note != "исходная заметка" {
		t.Errorf("Note changed unexpectedly: %v", updated.Task.Note)
	}
	if len(updated.Task.Tags) != 1 || updated.Task.Tags[0] != "один" {
		t.Errorf("Tags changed unexpectedly: %v", updated.Task.Tags)
	}

	// Version bumped exactly once.
	if updated.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", updated.Version, created.Version+1)
	}
}

func TestUpdateTask_AllFields(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	planned := task.CalendarDate{Year: 2026, Month: 12, Day: 24}
	due := task.CalendarDate{Year: 2026, Month: 12, Day: 25}
	dueAt := testNow.Add(72 * time.Hour)

	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title:       Some("полное обновление"),
		Note:        Some("заметка"),
		Priority:    Some(task.PriorityHigh),
		PlannedDate: Some(planned),
		DueDate:     Some(due),
		DueAt:       Some(dueAt),
		Timezone:    Some("America/New_York"),
		Tags:        Some([]string{"a", "b"}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	if updated.Task.Title != "полное обновление" {
		t.Errorf("Title = %q", updated.Task.Title)
	}
	if updated.Task.Note == nil || *updated.Task.Note != "заметка" {
		t.Error("Note not set")
	}
	if updated.Task.Priority != task.PriorityHigh {
		t.Errorf("Priority = %v", updated.Task.Priority)
	}
	if updated.Task.PlannedDate == nil || *updated.Task.PlannedDate != planned {
		t.Error("PlannedDate not set")
	}
	if updated.Task.DueDate == nil || *updated.Task.DueDate != due {
		t.Error("DueDate not set")
	}
	if updated.Task.DueAt == nil || !updated.Task.DueAt.Equal(dueAt) {
		t.Error("DueAt not set")
	}
	if updated.Task.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q", updated.Task.Timezone)
	}
	if len(updated.Task.Tags) != 2 {
		t.Errorf("Tags = %v", updated.Task.Tags)
	}
}

func TestUpdateTask_ClearNullableFields(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:       "x",
		Note:        Some("заметка"),
		PlannedDate: Some(task.CalendarDate{Year: 2026, Month: 12, Day: 1}),
		DueDate:     Some(task.CalendarDate{Year: 2026, Month: 12, Day: 2}),
		DueAt:       Some(testNow.Add(time.Hour)),
		Tags:        []string{"тэг"},
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	empty := ""
	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Note:        Some(empty),
		PlannedDate: Some(task.CalendarDate{}),
		DueDate:     Some(task.CalendarDate{}),
		Tags:        Some([]string{}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	if updated.Task.Note != nil {
		t.Errorf("Note = %v, want nil after clearing", updated.Task.Note)
	}
	if updated.Task.PlannedDate != nil {
		t.Errorf("PlannedDate = %v, want nil after clearing", updated.Task.PlannedDate)
	}
	if updated.Task.DueDate != nil {
		t.Errorf("DueDate = %v, want nil after clearing", updated.Task.DueDate)
	}
	if len(updated.Task.Tags) != 0 {
		t.Errorf("Tags = %v, want empty after clearing", updated.Task.Tags)
	}
	// Untouched field survives.
	if updated.Task.DueAt == nil {
		t.Error("DueAt must survive an update that does not mention it")
	}
}

func TestUpdateTask_DomainValidationError(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	_, err = svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title: Some(""),
	}, task.ActorUser)
	if !errors.Is(err, task.ErrInvalidTitle) {
		t.Fatalf("error = %v, want ErrInvalidTitle", err)
	}

	// The failed update must not have changed anything.
	after, err := svc.GetTask(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if after.Task.Title != "x" {
		t.Errorf("Title = %q, want unchanged", after.Task.Title)
	}
	if after.Version != created.Version {
		t.Errorf("Version = %d, want unchanged %d", after.Version, created.Version)
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.UpdateTask(context.Background(), 12345, 1, UpdateTaskInput{Title: Some("x")}, task.ActorUser)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("error = %v, want ErrTaskNotFound", err)
	}
}

// --- Complete / Cancel / Restore ---------------------------------------------

func TestCompleteTask(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	completed, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser)
	if err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}
	if completed.Task.Status != task.StatusCompleted {
		t.Errorf("Status = %v, want completed", completed.Task.Status)
	}
	if completed.Task.CompletedAt == nil {
		t.Error("CompletedAt must be set")
	}
	if completed.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", completed.Version, created.Version+1)
	}

	events := uow.Events()
	last := events[len(events)-1]
	if last.Type != task.EventTypeCompleted {
		t.Errorf("event type = %v, want completed", last.Type)
	}
}

func TestCompleteTask_InvalidTransition(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	// Already completed: the domain transition error must surface unchanged.
	_, err = svc.CompleteTask(context.Background(), created.Task.ID, created.Version+1, task.ActorUser)
	if err == nil {
		t.Fatal("expected an error for completing a completed task")
	}
	if !task.IsInvalidTransitionError(err) {
		t.Fatalf("error = %v (%T), want a domain InvalidTransitionError", err, err)
	}
}

func TestCancelTask(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	cancelled, err := svc.CancelTask(context.Background(), created.Task.ID, created.Version, task.ActorUser)
	if err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}
	if cancelled.Task.Status != task.StatusCancelled {
		t.Errorf("Status = %v, want cancelled", cancelled.Task.Status)
	}
	if cancelled.Task.CancelledAt == nil {
		t.Error("CancelledAt must be set")
	}

	events := uow.Events()
	if events[len(events)-1].Type != task.EventTypeCancelled {
		t.Errorf("event type = %v, want cancelled", events[len(events)-1].Type)
	}
}

func TestRestoreTask(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	restored, err := svc.RestoreTask(context.Background(), created.Task.ID, created.Version+1, task.ActorUser)
	if err != nil {
		t.Fatalf("RestoreTask failed: %v", err)
	}
	if restored.Task.Status != task.StatusOpen {
		t.Errorf("Status = %v, want open", restored.Task.Status)
	}
	if restored.Task.CompletedAt != nil {
		t.Error("CompletedAt must be cleared by restore")
	}

	events := uow.Events()
	if events[len(events)-1].Type != task.EventTypeRestored {
		t.Errorf("event type = %v, want restored", events[len(events)-1].Type)
	}
}

// --- RescheduleTask -----------------------------------------------------------

func TestRescheduleTask(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	due := task.CalendarDate{Year: 2027, Month: 1, Day: 15}
	rescheduled, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{
		DueDate: Some(due),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("RescheduleTask failed: %v", err)
	}
	if rescheduled.Task.DueDate == nil || *rescheduled.Task.DueDate != due {
		t.Errorf("DueDate = %v, want %v", rescheduled.Task.DueDate, due)
	}
	if rescheduled.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", rescheduled.Version, created.Version+1)
	}

	events := uow.Events()
	last := events[len(events)-1]
	if last.Type != task.EventTypeRescheduled {
		t.Errorf("event type = %v, want rescheduled", last.Type)
	}
	// A single changed field keeps the documented single-field shape.
	var payload map[string]any
	if err := last.PayloadAs(&payload); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if payload["field"] != "due_date" {
		t.Errorf("payload field = %v, want due_date", payload["field"])
	}
	if payload["old"] != nil {
		t.Errorf("payload old = %v, want nil (no due date before)", payload["old"])
	}
	if payload["new"] != "2027-01-15" {
		t.Errorf("payload new = %v, want 2027-01-15", payload["new"])
	}
}

func TestRescheduleTask_ClosedTaskRejected(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	_, err = svc.RescheduleTask(context.Background(), created.Task.ID, created.Version+1, RescheduleTaskInput{
		DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1}),
	}, task.ActorUser)
	if !task.IsInvalidTransitionError(err) {
		t.Fatalf("error = %v (%T), want a domain InvalidTransitionError", err, err)
	}
}

// --- Optimistic locking -------------------------------------------------------

func TestVersionStartsAtOne(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("Version = %d, want 1", created.Version)
	}
}

func TestEachMutationBumpsVersionOnce(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	version := created.Version
	steps := []struct {
		name string
		run  func() (TaskWithVersion, error)
	}{
		{"update", func() (TaskWithVersion, error) {
			return svc.UpdateTask(context.Background(), created.Task.ID, version, UpdateTaskInput{Note: Some("n")}, task.ActorUser)
		}},
		{"complete", func() (TaskWithVersion, error) {
			return svc.CompleteTask(context.Background(), created.Task.ID, version, task.ActorUser)
		}},
		{"restore", func() (TaskWithVersion, error) {
			return svc.RestoreTask(context.Background(), created.Task.ID, version, task.ActorUser)
		}},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			result, err := step.run()
			if err != nil {
				t.Fatalf("%s failed: %v", step.name, err)
			}
			if result.Version != version+1 {
				t.Fatalf("Version = %d, want exactly %d", result.Version, version+1)
			}
			version = result.Version
		})
	}
}

func TestReadDoesNotBumpVersion(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := svc.GetTask(context.Background(), created.Task.ID); err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		if _, err := svc.ListTasks(context.Background(), TaskFilter{}); err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
	}

	found, err := svc.GetTask(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if found.Version != created.Version {
		t.Errorf("Version = %d, want %d after repeated reads", found.Version, created.Version)
	}
}

func TestStaleVersionReturnsConflict(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// First writer succeeds with version 1.
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, 1, UpdateTaskInput{Note: Some("first")}, task.ActorUser); err != nil {
		t.Fatalf("first UpdateTask failed: %v", err)
	}

	// Second writer still holds version 1.
	_, err = svc.UpdateTask(context.Background(), created.Task.ID, 1, UpdateTaskInput{Note: Some("second")}, task.ActorUser)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, want ErrVersionConflict", err)
	}
	if !VersionConflict(err) {
		t.Error("VersionConflict(err) must report true")
	}
}

func TestVersionConflictOnMutations(t *testing.T) {
	// bumpNote moves the stored version forward without closing the task.
	bumpNote := func(svc *Service, id int64, version int) error {
		_, err := svc.UpdateTask(context.Background(), id, version, UpdateTaskInput{Note: Some("bump")}, task.ActorUser)
		return err
	}
	// completeThenRestore leaves the task completed, so a restore is a legal
	// transition that can reach the version check.
	complete := func(svc *Service, id int64, version int) error {
		_, err := svc.CompleteTask(context.Background(), id, version, task.ActorUser)
		return err
	}

	tests := []struct {
		name  string
		setup func(svc *Service, id int64, version int) error
		run   func(svc *Service, id int64, version int) error
	}{
		{"update", bumpNote, func(svc *Service, id int64, version int) error {
			_, err := svc.UpdateTask(context.Background(), id, version, UpdateTaskInput{Note: Some("n")}, task.ActorUser)
			return err
		}},
		{"complete", bumpNote, func(svc *Service, id int64, version int) error {
			_, err := svc.CompleteTask(context.Background(), id, version, task.ActorUser)
			return err
		}},
		{"cancel", bumpNote, func(svc *Service, id int64, version int) error {
			_, err := svc.CancelTask(context.Background(), id, version, task.ActorUser)
			return err
		}},
		{"restore", complete, func(svc *Service, id int64, version int) error {
			_, err := svc.RestoreTask(context.Background(), id, version, task.ActorUser)
			return err
		}},
		{"reschedule", bumpNote, func(svc *Service, id int64, version int) error {
			_, err := svc.RescheduleTask(context.Background(), id, version, RescheduleTaskInput{
				DueDate: Some(task.CalendarDate{Year: 2027, Month: 5, Day: 5}),
			}, task.ActorUser)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
			if err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			// Move the stored version forward, then use the stale one.
			if err := tt.setup(svc, created.Task.ID, created.Version); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			if err := tt.run(svc, created.Task.ID, created.Version); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("error = %v, want ErrVersionConflict", err)
			}
		})
	}
}

func TestMissingTaskReturnsNotFound(t *testing.T) {
	tests := []struct {
		name string
		run  func(svc *Service) error
	}{
		{"get", func(svc *Service) error {
			_, err := svc.GetTask(context.Background(), 404)
			return err
		}},
		{"update", func(svc *Service) error {
			_, err := svc.UpdateTask(context.Background(), 404, 1, UpdateTaskInput{Note: Some("n")}, task.ActorUser)
			return err
		}},
		{"complete", func(svc *Service) error {
			_, err := svc.CompleteTask(context.Background(), 404, 1, task.ActorUser)
			return err
		}},
		{"cancel", func(svc *Service) error {
			_, err := svc.CancelTask(context.Background(), 404, 1, task.ActorUser)
			return err
		}},
		{"restore", func(svc *Service) error {
			_, err := svc.RestoreTask(context.Background(), 404, 1, task.ActorUser)
			return err
		}},
		{"reschedule", func(svc *Service) error {
			_, err := svc.RescheduleTask(context.Background(), 404, 1, RescheduleTaskInput{
				DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1}),
			}, task.ActorUser)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			if err := tt.run(svc); !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("error = %v, want ErrTaskNotFound", err)
			}
		})
	}
}
