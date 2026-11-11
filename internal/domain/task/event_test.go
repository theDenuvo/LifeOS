package task

import (
	"testing"
)

func TestEventType_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		eType EventType
		exp   bool
	}{
		{"created", EventTypeCreated, true},
		{"updated", EventTypeUpdated, true},
		{"completed", EventTypeCompleted, true},
		{"cancelled", EventTypeCancelled, true},
		{"rescheduled", EventTypeRescheduled, true},
		{"restored", EventTypeRestored, true},
		{"linked", EventTypeLinked, true},
		{"converted_from_inbox", EventTypeConvertedFromInbox, true},
		{"invalid", EventType("invalid"), false},
		{"empty", EventType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.eType.IsValid(); got != tt.exp {
				t.Errorf("EventType.IsValid() = %v, want %v", got, tt.exp)
			}
		})
	}
}

func TestEventType_String(t *testing.T) {
	if EventTypeCreated.String() != "created" {
		t.Errorf("EventTypeCreated.String() = %q, want \"created\"", EventTypeCreated.String())
	}
}

func TestActor_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		actor Actor
		exp   bool
	}{
		{"user", ActorUser, true},
		{"assistant", ActorAssistant, true},
		{"system", ActorSystem, true},
		{"invalid", Actor("invalid"), false},
		{"empty", Actor(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.actor.IsValid(); got != tt.exp {
				t.Errorf("Actor.IsValid() = %v, want %v", got, tt.exp)
			}
		})
	}
}

func TestNewTaskEvent(t *testing.T) {
	payload := map[string]string{"field": "priority", "old": "medium", "new": "high"}
	event, err := NewTaskEvent(1, EventTypeUpdated, payload, ActorUser)
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
	if event.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// Verify payload
	var result map[string]string
	if err := event.PayloadAs(&result); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if result["field"] != "priority" {
		t.Errorf("payload field = %q, want \"priority\"", result["field"])
	}
}

func TestNewTaskEvent_InvalidPayload(t *testing.T) {
	// Create a payload that can't be marshaled (e.g., channel)
	_, err := NewTaskEvent(1, EventTypeUpdated, make(chan int), ActorUser)
	if err == nil {
		t.Error("expected error for non-marshalable payload")
	}
}

func TestTaskEvent_PayloadAs(t *testing.T) {
	payload := map[string]int{"count": 42}
	event, _ := NewTaskEvent(1, EventTypeCreated, payload, ActorSystem)

	var result map[string]int
	if err := event.PayloadAs(&result); err != nil {
		t.Fatalf("PayloadAs failed: %v", err)
	}
	if result["count"] != 42 {
		t.Errorf("payload count = %d, want 42", result["count"])
	}
}

func TestValidEventTypes(t *testing.T) {
	types := ValidEventTypes()
	expected := 8
	if len(types) != expected {
		t.Errorf("ValidEventTypes() returned %d types, want %d", len(types), expected)
	}
}

func TestValidActors(t *testing.T) {
	actors := ValidActors()
	expected := 3
	if len(actors) != expected {
		t.Errorf("ValidActors() returned %d actors, want %d", len(actors), expected)
	}
}
