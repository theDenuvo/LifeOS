package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

type stepClock struct {
	times []time.Time
	index int
}

func (c *stepClock) Now() time.Time {
	if len(c.times) == 0 {
		return time.Time{}
	}
	if c.index >= len(c.times) {
		return c.times[len(c.times)-1]
	}
	now := c.times[c.index]
	c.index++
	return now
}

func TestUpdateTaskClearingOnlyDueAtAdvancesUpdatedAt(t *testing.T) {
	t1 := testNow
	t2 := t1.Add(5 * time.Minute)
	uow := NewFakeUnitOfWork()
	svc := NewService(uow, &stepClock{times: []time.Time{t1, t2}})
	oldDueAt := t1.Add(time.Hour).Add(123456789 * time.Nanosecond)

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "clear exact deadline",
		DueAt: Some(oldDueAt),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version,
		UpdateTaskInput{DueAt: Some(time.Time{})}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	if updated.Task.DueAt != nil {
		t.Fatalf("DueAt = %v, want nil", updated.Task.DueAt)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", updated.Version, created.Version+1)
	}
	if !updated.Task.UpdatedAt.Equal(t2) {
		t.Errorf("UpdatedAt = %v, want %v", updated.Task.UpdatedAt, t2)
	}
	payload := decodePayload(t, lastEvent(t, uow))
	changes := decodeChanges(t, payload)
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1: %v", len(changes), payload)
	}
	if changes[0]["old"] != oldDueAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("old = %v, want exact timestamp %s", changes[0]["old"], oldDueAt.UTC().Format(time.RFC3339Nano))
	}
	if changes[0]["new"] != nil {
		t.Errorf("new = %v, want null", changes[0]["new"])
	}
}

func TestUpdateEventPayloadPreservesDueAtNanoseconds(t *testing.T) {
	svc, uow := newTestService(t)
	oldDueAt := time.Date(2026, 11, 13, 10, 11, 12, 123456789, time.UTC)
	newDueAt := time.Date(2026, 11, 14, 13, 14, 15, 987654321, time.UTC)

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "nanosecond audit",
		DueAt: Some(oldDueAt),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	_, err = svc.UpdateTask(context.Background(), created.Task.ID, created.Version,
		UpdateTaskInput{DueAt: Some(newDueAt)}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	payload := decodePayload(t, lastEvent(t, uow))
	changes := decodeChanges(t, payload)
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1: %v", len(changes), payload)
	}

	wantOld := oldDueAt.UTC().Format(time.RFC3339Nano)
	wantNew := newDueAt.UTC().Format(time.RFC3339Nano)
	if changes[0]["old"] != wantOld {
		t.Errorf("old = %v, want %s", changes[0]["old"], wantOld)
	}
	if changes[0]["new"] != wantNew {
		t.Errorf("new = %v, want %s", changes[0]["new"], wantNew)
	}

	for name, raw := range map[string]string{"old": wantOld, "new": wantNew} {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			t.Fatalf("parse %s timestamp %q: %v", name, raw, err)
		}
		expected := oldDueAt
		if name == "new" {
			expected = newDueAt
		}
		if !parsed.Equal(expected) {
			t.Errorf("%s timestamp parsed as %s, want exact %s", name, parsed, expected)
		}
	}
}

func TestEmptyUpdateTaskIsNoOpAndChecksExpectedVersion(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "unchanged"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	before := uow.FindTaskMust(t, created.Task.ID)
	eventCount := uow.EventCount()

	got, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{}, task.ActorUser)
	if err != nil {
		t.Fatalf("empty UpdateTask failed: %v", err)
	}
	if got.Version != created.Version {
		t.Errorf("Version = %d, want unchanged %d", got.Version, created.Version)
	}
	if !got.Task.UpdatedAt.Equal(before.Task.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged %v", got.Task.UpdatedAt, before.Task.UpdatedAt)
	}
	if after := uow.FindTaskMust(t, created.Task.ID); after.Version != before.Version {
		t.Errorf("stored Version = %d, want unchanged %d", after.Version, before.Version)
	}
	if uow.EventCount() != eventCount {
		t.Errorf("events = %d, want unchanged %d", uow.EventCount(), eventCount)
	}

	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version+1, UpdateTaskInput{}, task.ActorUser); !errors.Is(err, ErrVersionConflict) {
		t.Errorf("stale empty update error = %v, want ErrVersionConflict", err)
	}
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID+1000, created.Version, UpdateTaskInput{}, task.ActorUser); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing empty update error = %v, want ErrTaskNotFound", err)
	}
	if uow.EventCount() != eventCount {
		t.Errorf("events after rejected no-op requests = %d, want %d", uow.EventCount(), eventCount)
	}
}
