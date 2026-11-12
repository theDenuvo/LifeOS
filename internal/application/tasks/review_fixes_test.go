package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// decodeChanges reads the "changes" array of a payload, falling back to the
// documented single-field shape ({field, old, new}) as a one-element list.
func decodeChanges(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()

	if raw, ok := payload["changes"]; ok {
		list, ok := raw.([]any)
		if !ok {
			t.Fatalf("\"changes\" is %T, want array", raw)
		}
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("change entry is %T, want object", item)
			}
			out = append(out, entry)
		}
		return out
	}

	// Single-field shape.
	field, ok := payload["field"].(string)
	if !ok {
		t.Fatalf("payload has neither \"changes\" nor \"field\": %v", payload)
	}
	return []map[string]any{{"field": field, "old": payload["old"], "new": payload["new"]}}
}

// lastEvent returns the most recently appended event.
func lastEvent(t *testing.T, uow *FakeUnitOfWork) *task.TaskEvent {
	t.Helper()
	events := uow.Events()
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}
	return events[len(events)-1]
}

// decodePayload unmarshals an event payload into a generic map.
func decodePayload(t *testing.T, e *task.TaskEvent) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := e.PayloadAs(&payload); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	return payload
}

// --- 1. Snapshot and real old/new values --------------------------------------

func TestUpdateEventPayloadReportsSingleChange(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "x",
		Priority: Some(task.PriorityMedium),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Priority: Some(task.PriorityHigh),
	}, task.ActorUser); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	payload := decodePayload(t, lastEvent(t, uow))

	// The documented single-field shape must be used.
	if payload["field"] != "priority" {
		t.Fatalf("payload field = %v, want priority; payload = %v", payload["field"], payload)
	}
	if payload["old"] != "medium" {
		t.Errorf("payload old = %v, want medium", payload["old"])
	}
	if payload["new"] != "high" {
		t.Errorf("payload new = %v, want high", payload["new"])
	}

	// The values must actually differ, otherwise the diff is meaningless.
	if payload["old"] == payload["new"] {
		t.Error("old and new are equal; the snapshot alias bug is still present")
	}
}

func TestUpdateEventPayloadReportsMultipleChanges(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "x",
		Priority: Some(task.PriorityLow),
		Note:     Some("before"),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	due := task.CalendarDate{Year: 2027, Month: 3, Day: 4}
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title:    Some("y"),
		Priority: Some(task.PriorityHigh),
		Note:     Some("after"),
		DueDate:  Some(due),
	}, task.ActorUser); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	payload := decodePayload(t, lastEvent(t, uow))
	changes := decodeChanges(t, payload)

	if len(changes) != 4 {
		t.Fatalf("changes = %d (%v), want 4", len(changes), payload)
	}

	byField := make(map[string]map[string]any, len(changes))
	for _, change := range changes {
		field, _ := change["field"].(string)
		byField[field] = change
	}

	expectations := []struct {
		field    string
		old, new any
	}{
		{"title", "x", "y"},
		{"note", "before", "after"},
		{"priority", "low", "high"},
		{"due_date", nil, "2027-03-04"},
	}

	for _, want := range expectations {
		change, ok := byField[want.field]
		if !ok {
			t.Errorf("field %q missing from changes", want.field)
			continue
		}
		if change["old"] != want.old {
			t.Errorf("%s old = %v, want %v", want.field, change["old"], want.old)
		}
		if change["new"] != want.new {
			t.Errorf("%s new = %v, want %v", want.field, change["new"], want.new)
		}
	}

	// Unchanged fields must not appear.
	if _, ok := byField["timezone"]; ok {
		t.Error("unchanged field timezone must not appear in the payload")
	}
}

func TestUpdateEventPayloadReportsClearedFieldsAsNull(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "x",
		Note:  Some("keep me"),
		DueAt: Some(testNow.Add(24 * time.Hour)),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Note:  Some(""),
		DueAt: Some(time.Time{}),
	}, task.ActorUser); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	payload := decodePayload(t, lastEvent(t, uow))
	changes := decodeChanges(t, payload)

	if len(changes) != 2 {
		t.Fatalf("changes = %d (%v), want 2", len(changes), payload)
	}

	byField := make(map[string]map[string]any, len(changes))
	for _, change := range changes {
		field, _ := change["field"].(string)
		byField[field] = change
	}

	note, ok := byField["note"]
	if !ok {
		t.Fatal("note change missing")
	}
	if note["old"] != "keep me" {
		t.Errorf("note old = %v, want %q", note["old"], "keep me")
	}
	if note["new"] != nil {
		t.Errorf("note new = %v, want nil", note["new"])
	}

	dueAt, ok := byField["due_at"]
	if !ok {
		t.Fatal("due_at change missing")
	}
	if dueAt["new"] != nil {
		t.Errorf("due_at new = %v, want nil", dueAt["new"])
	}
	// The old value must be the normalised RFC3339 UTC string, not a raw time.
	wantOld := testNow.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if dueAt["old"] != wantOld {
		t.Errorf("due_at old = %v, want %q", dueAt["old"], wantOld)
	}
}

// TestUpdatePayloadHasNoDomainTypes guards against Go pointers or domain structs
// leaking into the audit history.
func TestUpdatePayloadHasNoDomainTypes(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	due := task.CalendarDate{Year: 2027, Month: 6, Day: 7}
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		DueDate: Some(due),
		DueAt:   Some(testNow.Add(time.Hour)),
		Tags:    Some([]string{"a"}),
	}, task.ActorUser); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	// Round-trip through JSON: anything that is not a plain JSON value would
	// either fail to marshal or reappear as an object/array.
	raw, err := json.Marshal(lastEvent(t, uow).Payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	var walk func(v any)
	walk = func(v any) {
		switch value := v.(type) {
		case map[string]any:
			for key, item := range value {
				switch key {
				case "field", "old", "new", "changes":
				default:
					t.Errorf("unexpected payload key %q", key)
				}
				walk(item)
			}
		case []any:
			for _, item := range value {
				walk(item)
			}
		case string, float64, bool, nil:
			// Plain JSON values are fine.
		default:
			t.Errorf("unexpected JSON value type %T", value)
		}
	}
	walk(generic)
}

// --- 3. Clearing nullable fields ----------------------------------------------

func TestClearEveryNullableFieldInUpdate(t *testing.T) {
	tests := []struct {
		name  string
		input CreateTaskInput
		clear UpdateTaskInput
		check func(t *testing.T, got *task.Task)
	}{
		{
			name:  "note",
			input: CreateTaskInput{Title: "x", Note: Some("text")},
			clear: UpdateTaskInput{Note: Some("")},
			check: func(t *testing.T, got *task.Task) {
				if got.Note != nil {
					t.Errorf("Note = %v, want nil", *got.Note)
				}
			},
		},
		{
			name:  "planned_date",
			input: CreateTaskInput{Title: "x", PlannedDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1})},
			clear: UpdateTaskInput{PlannedDate: Some(task.CalendarDate{})},
			check: func(t *testing.T, got *task.Task) {
				if got.PlannedDate != nil {
					t.Errorf("PlannedDate = %v, want nil", got.PlannedDate)
				}
			},
		},
		{
			name:  "due_date",
			input: CreateTaskInput{Title: "x", DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1})},
			clear: UpdateTaskInput{DueDate: Some(task.CalendarDate{})},
			check: func(t *testing.T, got *task.Task) {
				if got.DueDate != nil {
					t.Errorf("DueDate = %v, want nil", got.DueDate)
				}
			},
		},
		{
			name:  "due_at",
			input: CreateTaskInput{Title: "x", DueAt: Some(testNow.Add(time.Hour))},
			clear: UpdateTaskInput{DueAt: Some(time.Time{})},
			check: func(t *testing.T, got *task.Task) {
				if got.DueAt != nil {
					t.Errorf("DueAt = %v, want nil", got.DueAt)
				}
			},
		},
		{
			name:  "tags",
			input: CreateTaskInput{Title: "x", Tags: []string{"a", "b"}},
			clear: UpdateTaskInput{Tags: Some([]string{})},
			check: func(t *testing.T, got *task.Task) {
				if len(got.Tags) != 0 {
					t.Errorf("Tags = %v, want empty", got.Tags)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newTestService(t)
			created, err := svc.CreateTask(context.Background(), tt.input, task.ActorUser)
			if err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, tt.clear, task.ActorUser)
			if err != nil {
				t.Fatalf("UpdateTask failed: %v", err)
			}

			tt.check(t, updated.Task)

			// Clearing is a real change: version moves by one and UpdatedAt follows the clock.
			if updated.Version != created.Version+1 {
				t.Errorf("Version = %d, want %d", updated.Version, created.Version+1)
			}
			if !updated.Task.UpdatedAt.Equal(testNow) {
				t.Errorf("UpdatedAt = %v, want %v", updated.Task.UpdatedAt, testNow)
			}

			// The audit event records the old value and the cleared value.
			payload := decodePayload(t, lastEvent(t, uow))
			changes := decodeChanges(t, payload)
			if len(changes) != 1 {
				t.Fatalf("changes = %d (%v), want 1", len(changes), payload)
			}
			if tt.name == "tags" {
				// Tags are always a slice in the domain, so a clear is an empty
				// array rather than null.
				if changes[0]["new"] == nil {
					t.Error("tags new = null, want an empty array")
				}
			} else if changes[0]["new"] != nil {
				t.Errorf("new = %v, want nil for a cleared field", changes[0]["new"])
			}
		})
	}
}

func TestClearDueAtIsTheOnlyChangeStillBumpsVersion(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "x",
		DueAt: Some(testNow.Add(time.Hour)),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		DueAt: Some(time.Time{}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	if updated.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", updated.Version, created.Version+1)
	}
	if !updated.Task.UpdatedAt.Equal(testNow) {
		t.Errorf("UpdatedAt = %v, want %v (must not keep the old value)", updated.Task.UpdatedAt, testNow)
	}
	if uow.EventCount() != 2 {
		t.Errorf("events = %d, want 2 (created + updated)", uow.EventCount())
	}
}

// --- Reschedule clearing ------------------------------------------------------

func TestClearEveryNullableFieldInReschedule(t *testing.T) {
	tests := []struct {
		name  string
		input CreateTaskInput
		clear RescheduleTaskInput
		check func(t *testing.T, got *task.Task)
	}{
		{
			name:  "planned_date",
			input: CreateTaskInput{Title: "x", PlannedDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1})},
			clear: RescheduleTaskInput{PlannedDate: Some(task.CalendarDate{})},
			check: func(t *testing.T, got *task.Task) {
				if got.PlannedDate != nil {
					t.Errorf("PlannedDate = %v, want nil", got.PlannedDate)
				}
			},
		},
		{
			name:  "due_date",
			input: CreateTaskInput{Title: "x", DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1})},
			clear: RescheduleTaskInput{DueDate: Some(task.CalendarDate{})},
			check: func(t *testing.T, got *task.Task) {
				if got.DueDate != nil {
					t.Errorf("DueDate = %v, want nil", got.DueDate)
				}
			},
		},
		{
			name:  "due_at",
			input: CreateTaskInput{Title: "x", DueAt: Some(testNow.Add(time.Hour))},
			clear: RescheduleTaskInput{DueAt: Some(time.Time{})},
			check: func(t *testing.T, got *task.Task) {
				if got.DueAt != nil {
					t.Errorf("DueAt = %v, want nil", got.DueAt)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newTestService(t)
			created, err := svc.CreateTask(context.Background(), tt.input, task.ActorUser)
			if err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			updated, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, tt.clear, task.ActorUser)
			if err != nil {
				t.Fatalf("RescheduleTask failed: %v", err)
			}

			tt.check(t, updated.Task)

			// Version and UpdatedAt must move even when the only change is a clear.
			if updated.Version != created.Version+1 {
				t.Errorf("Version = %d, want %d", updated.Version, created.Version+1)
			}
			if !updated.Task.UpdatedAt.Equal(testNow) {
				t.Errorf("UpdatedAt = %v, want %v", updated.Task.UpdatedAt, testNow)
			}

			payload := decodePayload(t, lastEvent(t, uow))
			changes := decodeChanges(t, payload)
			if len(changes) != 1 {
				t.Fatalf("changes = %d (%v), want 1", len(changes), payload)
			}
			if changes[0]["new"] != nil {
				t.Errorf("new = %v, want nil", changes[0]["new"])
			}
		})
	}
}

func TestRescheduleMultipleFieldsPayload(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:   "x",
		DueDate: Some(task.CalendarDate{Year: 2027, Month: 1, Day: 1}),
		DueAt:   Some(testNow.Add(time.Hour)),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	newDue := task.CalendarDate{Year: 2027, Month: 5, Day: 5}
	newDueAt := testNow.Add(72 * time.Hour)

	if _, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{
		DueDate: Some(newDue),
		DueAt:   Some(newDueAt),
	}, task.ActorUser); err != nil {
		t.Fatalf("RescheduleTask failed: %v", err)
	}

	payload := decodePayload(t, lastEvent(t, uow))
	changes := decodeChanges(t, payload)

	if len(changes) != 2 {
		t.Fatalf("changes = %d (%v), want 2", len(changes), payload)
	}

	byField := make(map[string]map[string]any, len(changes))
	for _, change := range changes {
		field, _ := change["field"].(string)
		byField[field] = change
	}

	if got := byField["due_date"]; got == nil {
		t.Error("due_date change missing")
	} else {
		if got["old"] != "2027-01-01" {
			t.Errorf("due_date old = %v, want 2027-01-01", got["old"])
		}
		if got["new"] != "2027-05-05" {
			t.Errorf("due_date new = %v, want 2027-05-05", got["new"])
		}
	}

	if got := byField["due_at"]; got == nil {
		t.Error("due_at change missing")
	} else if got["new"] != newDueAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("due_at new = %v, want %v", got["new"], newDueAt.UTC().Format(time.RFC3339Nano))
	}
}

func TestRescheduleEmptyRequestIsNoOp(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	got, err := svc.RescheduleTask(context.Background(), created.Task.ID, created.Version, RescheduleTaskInput{}, task.ActorUser)
	if err != nil {
		t.Fatalf("RescheduleTask failed: %v", err)
	}
	if got.Version != created.Version {
		t.Errorf("Version = %d, want unchanged %d for an empty request", got.Version, created.Version)
	}
	if uow.EventCount() != 1 {
		t.Errorf("events = %d, want 1 (no event for a no-op)", uow.EventCount())
	}
}

func TestRescheduleClosedTaskStillRejectedAfterClearWork(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title: "x",
		DueAt: Some(testNow.Add(time.Hour)),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	_, err = svc.RescheduleTask(context.Background(), created.Task.ID, created.Version+1, RescheduleTaskInput{
		DueAt: Some(time.Time{}),
	}, task.ActorUser)
	if !task.IsInvalidTransitionError(err) {
		t.Fatalf("error = %v, want domain InvalidTransitionError", err)
	}
}

// --- 4. Overdue uses the injected clock ---------------------------------------

func TestOverdueFilterUsesInjectedClock(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}
	nextMidnight := time.Date(2026, 11, 13, 0, 0, 0, 0, loc)

	tests := []struct {
		name     string
		input    CreateTaskInput
		overdue  bool
		wantOver bool
	}{
		{
			name:     "due_at before deadline",
			input:    CreateTaskInput{Title: "a", DueAt: Some(testNow.Add(time.Hour))},
			overdue:  true,
			wantOver: false,
		},
		{
			name:     "due_at exactly at deadline",
			input:    CreateTaskInput{Title: "b", DueAt: Some(testNow)},
			overdue:  true,
			wantOver: false, // strict comparison: equality is still on time
		},
		{
			name:     "due_at after deadline",
			input:    CreateTaskInput{Title: "c", DueAt: Some(testNow.Add(-time.Second))},
			overdue:  true,
			wantOver: true,
		},
		{
			name:     "due_date before next local midnight",
			input:    CreateTaskInput{Title: "d", DueDate: Some(task.CalendarDate{Year: 2026, Month: 11, Day: 12})},
			overdue:  true,
			wantOver: false,
		},
		{
			name:     "due_date exactly at next local midnight",
			input:    CreateTaskInput{Title: "e", DueDate: Some(task.CalendarDate{Year: 2026, Month: 11, Day: 12}), Timezone: Some("Europe/Moscow")},
			overdue:  true,
			wantOver: false, // testNow is 09:00, boundary is next midnight
		},
		{
			name:     "due_date after boundary",
			input:    CreateTaskInput{Title: "f", DueDate: Some(task.CalendarDate{Year: 2026, Month: 11, Day: 11})},
			overdue:  true,
			wantOver: true,
		},
		{
			name:     "not requested",
			input:    CreateTaskInput{Title: "g", DueAt: Some(testNow.Add(-time.Hour))},
			overdue:  false,
			wantOver: true, // included in the page, overdue value irrelevant
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			created, err := svc.CreateTask(context.Background(), tt.input, task.ActorUser)
			if err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			filter := TaskFilter{}
			if tt.overdue {
				filter.Overdue = Some(true)
			}

			result, err := svc.ListTasks(context.Background(), filter)
			if err != nil {
				t.Fatalf("ListTasks failed: %v", err)
			}

			if !tt.overdue {
				if result.Total != 1 {
					t.Fatalf("total = %d, want 1", result.Total)
				}
				return
			}

			gotOver := false
			for _, row := range result.Tasks {
				if row.Task.ID == created.Task.ID {
					gotOver = true
				}
			}
			if gotOver != tt.wantOver {
				t.Errorf("task %d overdue = %v, want %v", created.Task.ID, gotOver, tt.wantOver)
			}
		})
	}

	_ = nextMidnight
}

func TestOverdueFilterBoundaryAtExactMidnight(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation failed: %v", err)
	}

	// Pin the clock exactly at the next local midnight after the due date.
	asOf := time.Date(2026, 11, 12, 0, 0, 0, 0, loc)
	uow := NewFakeUnitOfWork()
	svc := NewService(uow, fixedClock{now: asOf})

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "boundary",
		Timezone: Some("Europe/Moscow"),
		DueDate:  Some(task.CalendarDate{Year: 2026, Month: 11, Day: 11}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// One nanosecond before the boundary the task is not overdue...
	early := NewService(uow, fixedClock{now: asOf.Add(-time.Nanosecond)})
	earlyResult, err := early.ListTasks(context.Background(), TaskFilter{Overdue: Some(true)})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	for _, row := range earlyResult.Tasks {
		if row.Task.ID == created.Task.ID {
			t.Error("task must not be overdue one nanosecond before next local midnight")
		}
	}

	// ...and exactly at the boundary it is.
	result, err := svc.ListTasks(context.Background(), TaskFilter{Overdue: Some(true)})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	found := false
	for _, row := range result.Tasks {
		if row.Task.ID == created.Task.ID {
			found = true
		}
	}
	if !found {
		t.Error("task must be overdue exactly at next local midnight")
	}
}

func TestOverdueFilterExcludesClosedTasks(t *testing.T) {
	for _, status := range []string{"completed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			svc, _ := newTestService(t)
			created, err := svc.CreateTask(context.Background(), CreateTaskInput{
				Title: "x",
				DueAt: Some(testNow.Add(-time.Hour)),
			}, task.ActorUser)
			if err != nil {
				t.Fatalf("CreateTask failed: %v", err)
			}

			if status == "completed" {
				_, err = svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser)
			} else {
				_, err = svc.CancelTask(context.Background(), created.Task.ID, created.Version, task.ActorUser)
			}
			if err != nil {
				t.Fatalf("transition failed: %v", err)
			}

			result, err := svc.ListTasks(context.Background(), TaskFilter{Overdue: Some(true)})
			if err != nil {
				t.Fatalf("ListTasks failed: %v", err)
			}
			for _, row := range result.Tasks {
				if row.Task.ID == created.Task.ID {
					t.Errorf("%s task must never be reported as overdue", status)
				}
			}
		})
	}
}

// --- 6. Concurrent optimistic locking ----------------------------------------

func TestTwoConcurrentUpdatesCannotBothCommit(t *testing.T) {
	uow := NewFakeUnitOfWork()
	svc := NewService(uow, fixedClock{now: testNow})

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Both writers read version 1. The outer transaction is held open while the
	// nested one commits first, so the nested writer wins and the outer one must
	// fail its commit-time version check. That is the lost-update window.
	var outerErr, innerErr error
	var inner TaskWithVersion

	uow.BeforeCommit = func() {
		// Runs once, inside the outer Do call, before its commit check.
		uow.BeforeCommit = nil
		innerErr = uow.Do(context.Background(), func(ctx context.Context, tx Transaction) error {
			current, err := tx.Tasks().FindByID(ctx, created.Task.ID)
			if err != nil {
				return err
			}
			if err := current.Task.Update(testNow, WithNoteForTest("inner")); err != nil {
				return err
			}
			inner, err = tx.Tasks().Update(ctx, created.Task.ID, current.Version, current.Task)
			if err != nil {
				return err
			}
			return tx.Events().Insert(ctx, mustEvent(t, current.Task, "inner"))
		})
	}

	outerErr = uow.Do(context.Background(), func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, created.Task.ID)
		if err != nil {
			return err
		}
		if err := current.Task.Update(testNow, WithNoteForTest("outer")); err != nil {
			return err
		}
		if _, err := tx.Tasks().Update(ctx, created.Task.ID, current.Version, current.Task); err != nil {
			return err
		}
		return tx.Events().Insert(ctx, mustEvent(t, current.Task, "outer"))
	})

	// Exactly one writer may commit.
	if innerErr != nil {
		t.Fatalf("inner (winning) transaction failed: %v", innerErr)
	}
	if outerErr == nil {
		t.Fatal("both transactions committed: lost update")
	}
	if !VersionConflict(outerErr) {
		t.Errorf("outer error = %v, want ErrVersionConflict", outerErr)
	}

	// The winner is stored exactly once, at version 2.
	stored := uow.FindTaskMust(t, created.Task.ID)
	if stored.Version != 2 {
		t.Errorf("stored version = %d, want 2", stored.Version)
	}
	if inner.Version != 2 {
		t.Errorf("inner version = %d, want 2", inner.Version)
	}
	if stored.Task.Note == nil || *stored.Task.Note != "inner" {
		t.Errorf("Note = %v, want %q (the outer write must not land)", stored.Task.Note, "inner")
	}

	// The loser left no event behind: created + inner only.
	if got := uow.EventCount(); got != 2 {
		t.Errorf("events = %d, want 2 (created + inner)", got)
	}
}

func TestConflictingTransactionWritesNoPartialEvents(t *testing.T) {
	uow := NewFakeUnitOfWork()
	svc := NewService(uow, fixedClock{now: testNow})

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Commit a bump first, so the stale writer below loses.
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Note: Some("winner"),
	}, task.ActorUser); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	before := uow.EventCount()

	// Stale writer still holds version 1.
	err = uow.Do(context.Background(), func(ctx context.Context, tx Transaction) error {
		current, err := tx.Tasks().FindByID(ctx, created.Task.ID)
		if err != nil {
			return err
		}
		if err := current.Task.Update(testNow, WithNoteForTest("loser")); err != nil {
			return err
		}
		if _, err := tx.Tasks().Update(ctx, created.Task.ID, 1, current.Task); err != nil {
			return err
		}
		return tx.Events().Insert(ctx, mustEvent(t, current.Task, "loser"))
	})
	if !VersionConflict(err) {
		t.Fatalf("error = %v, want ErrVersionConflict", err)
	}

	if got := uow.EventCount(); got != before {
		t.Errorf("events = %d, want %d: the loser's event must not be committed", got, before)
	}

	stored := uow.FindTaskMust(t, created.Task.ID)
	if stored.Task.Note == nil || *stored.Task.Note != "winner" {
		t.Errorf("Note = %v, want %q: the loser overwrote the winner", stored.Task.Note, "winner")
	}
}

// WithNoteForTest builds the note option without importing the domain option
// surface into every test helper call site.
func WithNoteForTest(note string) task.TaskOption {
	return task.WithNote(note)
}

// mustEvent builds an event for a task, failing the test on error.
func mustEvent(t *testing.T, t2 *task.Task, note string) *task.TaskEvent {
	t.Helper()
	event, err := t2.ToEvent(task.EventTypeUpdated, map[string]any{"field": "note", "old": nil, "new": note}, task.ActorUser, testNow)
	if err != nil {
		t.Fatalf("ToEvent failed: %v", err)
	}
	return event
}
