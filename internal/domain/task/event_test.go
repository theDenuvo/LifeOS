package task

import (
	"errors"
	"testing"
	"time"
)

var eventNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestEventType_IsValid(t *testing.T) {
	for _, eType := range ValidEventTypes() {
		t.Run(string(eType), func(t *testing.T) {
			if !eType.IsValid() {
				t.Errorf("EventType(%q).IsValid() = false, want true", eType)
			}
		})
	}

	for _, invalid := range []EventType{"invalid", "", "CREATED", "unknown_event"} {
		t.Run("invalid_"+string(invalid), func(t *testing.T) {
			if invalid.IsValid() {
				t.Errorf("EventType(%q).IsValid() = true, want false", invalid)
			}
		})
	}
}

func TestEventType_String(t *testing.T) {
	if EventTypeCreated.String() != "created" {
		t.Errorf("EventTypeCreated.String() = %q, want %q", EventTypeCreated.String(), "created")
	}
	if EventTypeConvertedFromInbox.String() != "converted_from_inbox" {
		t.Errorf("unexpected String(): %q", EventTypeConvertedFromInbox.String())
	}
}

func TestActor_IsValid(t *testing.T) {
	for _, actor := range ValidActors() {
		t.Run(string(actor), func(t *testing.T) {
			if !actor.IsValid() {
				t.Errorf("Actor(%q).IsValid() = false, want true", actor)
			}
		})
	}

	for _, invalid := range []Actor{"invalid", "", "USER", "agent"} {
		t.Run("invalid_"+string(invalid), func(t *testing.T) {
			if invalid.IsValid() {
				t.Errorf("Actor(%q).IsValid() = true, want false", invalid)
			}
		})
	}
}

func TestNewTaskEvent(t *testing.T) {
	payload := map[string]string{"field": "priority", "old": "medium", "new": "high"}
	event, err := NewTaskEvent(1, EventTypeUpdated, payload, ActorUser, eventNow)
	if err != nil {
		t.Fatalf("NewTaskEvent failed: %v", err)
	}

	if event.TaskID != 1 {
		t.Errorf("TaskID = %d, want 1", event.TaskID)
	}
	if event.Type != EventTypeUpdated {
		t.Errorf("Type = %v, want %v", event.Type, EventTypeUpdated)
	}
	if event.Actor != ActorUser {
		t.Errorf("Actor = %v, want %v", event.Actor, ActorUser)
	}
	if !event.CreatedAt.Equal(eventNow) {
		t.Errorf("CreatedAt = %v, want the caller supplied %v", event.CreatedAt, eventNow)
	}
	if event.ID != 0 {
		t.Errorf("ID = %d, want 0 for a new event", event.ID)
	}

	var result map[string]string
	if err := event.PayloadAs(&result); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if result["field"] != "priority" {
		t.Errorf("payload field = %q, want %q", result["field"], "priority")
	}
}

// TestNewTaskEvent_NoHiddenClock guards the requirement that the domain never
// reads the system clock.
func TestNewTaskEvent_NoHiddenClock(t *testing.T) {
	past := time.Date(1999, 1, 2, 3, 4, 5, 6, time.UTC)
	event, err := NewTaskEvent(7, EventTypeCreated, nil, ActorSystem, past)
	if err != nil {
		t.Fatalf("NewTaskEvent failed: %v", err)
	}
	if !event.CreatedAt.Equal(past) {
		t.Errorf("CreatedAt = %v, want %v", event.CreatedAt, past)
	}
}

func TestNewTaskEvent_AllEventTypes(t *testing.T) {
	for _, eType := range ValidEventTypes() {
		t.Run(string(eType), func(t *testing.T) {
			event, err := NewTaskEvent(1, eType, map[string]int{"n": 1}, ActorSystem, eventNow)
			if err != nil {
				t.Fatalf("NewTaskEvent(%q) failed: %v", eType, err)
			}
			if event.Type != eType {
				t.Errorf("Type = %q, want %q", event.Type, eType)
			}
		})
	}
}

func TestNewTaskEvent_AllActors(t *testing.T) {
	for _, actor := range ValidActors() {
		t.Run(string(actor), func(t *testing.T) {
			event, err := NewTaskEvent(1, EventTypeCreated, nil, actor, eventNow)
			if err != nil {
				t.Fatalf("NewTaskEvent(actor=%q) failed: %v", actor, err)
			}
			if event.Actor != actor {
				t.Errorf("Actor = %q, want %q", event.Actor, actor)
			}
		})
	}
}

func TestNewTaskEvent_InvalidEventType(t *testing.T) {
	for _, eType := range []EventType{"invalid", "", "Created"} {
		t.Run("type_"+string(eType), func(t *testing.T) {
			_, err := NewTaskEvent(1, eType, nil, ActorUser, eventNow)
			if !errors.Is(err, ErrInvalidEventType) {
				t.Errorf("expected ErrInvalidEventType, got %v", err)
			}
		})
	}
}

func TestNewTaskEvent_InvalidActor(t *testing.T) {
	for _, actor := range []Actor{"invalid", "", "User"} {
		t.Run("actor_"+string(actor), func(t *testing.T) {
			_, err := NewTaskEvent(1, EventTypeCreated, nil, actor, eventNow)
			if !errors.Is(err, ErrInvalidActor) {
				t.Errorf("expected ErrInvalidActor, got %v", err)
			}
		})
	}
}

func TestNewTaskEvent_InvalidPayload(t *testing.T) {
	if _, err := NewTaskEvent(1, EventTypeUpdated, make(chan int), ActorUser, eventNow); err == nil {
		t.Error("expected error for non-marshalable payload")
	}
}

func TestTaskEvent_PayloadAs(t *testing.T) {
	payload := map[string]int{"count": 42}
	event, err := NewTaskEvent(1, EventTypeCreated, payload, ActorSystem, eventNow)
	if err != nil {
		t.Fatalf("NewTaskEvent failed: %v", err)
	}

	var result map[string]int
	if err := event.PayloadAs(&result); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if result["count"] != 42 {
		t.Errorf("payload count = %d, want 42", result["count"])
	}
}

func TestValidEventTypes(t *testing.T) {
	if got := len(ValidEventTypes()); got != 8 {
		t.Errorf("ValidEventTypes() returned %d types, want 8", got)
	}
}

func TestValidActors(t *testing.T) {
	if got := len(ValidActors()); got != 3 {
		t.Errorf("ValidActors() returned %d actors, want 3", got)
	}
}
