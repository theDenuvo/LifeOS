package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// An empty reschedule request is a read-only no-op, but it must still honour
// optimistic locking: a client that cached an old version has to see a
// conflict, not a success it would build its next request on.
func TestRescheduleTaskEmptyRequestChecksVersion(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Another writer moves the task on between the client's read and its call.
	touched, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{
		DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("seeding reschedule failed: %v", err)
	}

	_, err = svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{}, task.ActorUser)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v (%T), want ErrVersionConflict", err, err)
	}

	// The current version is accepted and stays a no-op: no version bump, no
	// event, UpdatedAt untouched.
	result, err := svc.RescheduleTask(context.Background(), created.Task.ID, touched.Version, RescheduleTaskInput{}, task.ActorUser)
	if err != nil {
		t.Fatalf("empty reschedule with current version failed: %v", err)
	}
	if result.Version != touched.Version {
		t.Fatalf("Version = %d, want unchanged %d", result.Version, touched.Version)
	}
	if uow.EventCount() != 2 { // created + the seeding reschedule
		t.Fatalf("events = %d, want 2", uow.EventCount())
	}
}

// A date-only reschedule replaces an existing exact deadline: due_at takes
// priority in the domain, so keeping it would leave the old instant in effect
// no matter which date the caller moved to.
func TestRescheduleTaskDateOnlyReplacesDueAt(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:  "x",
		DueAt:  Some(testNow.Add(48 * time.Hour)),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	rescheduled, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{
		DueDate: Some(task.CalendarDate{Year: 2027, Month: 2, Day: 1}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("RescheduleTask failed: %v", err)
	}
	if rescheduled.Task.DueDate == nil || rescheduled.Task.DueDate.String() != "2027-02-01" {
		t.Fatalf("DueDate = %v, want 2027-02-01", rescheduled.Task.DueDate)
	}
	if rescheduled.Task.DueAt != nil {
		t.Fatalf("DueAt = %v, want nil: the old exact deadline must not survive a date-only move", rescheduled.Task.DueAt)
	}
}

// An explicit due_at in the same request wins: only a date-only reschedule
// drops the exact time.
func TestRescheduleTaskWithExplicitDueAtKeepsBoth(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	exact := testNow.Add(72 * time.Hour)
	rescheduled, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{
		DueDate: Some(task.CalendarDate{Year: 2027, Month: 2, Day: 1}),
		DueAt:   Some(exact),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("RescheduleTask failed: %v", err)
	}
	if rescheduled.Task.DueAt == nil || !rescheduled.Task.DueAt.Equal(exact) {
		t.Fatalf("DueAt = %v, want %v", rescheduled.Task.DueAt, exact)
	}
	if rescheduled.Task.DueDate == nil || rescheduled.Task.DueDate.String() != "2027-02-01" {
		t.Fatalf("DueDate = %v, want 2027-02-01", rescheduled.Task.DueDate)
	}
}
