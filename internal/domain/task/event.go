package task

import (
	"encoding/json"
	"time"
)

// EventType represents the type of a task event.
type EventType string

const (
	EventTypeCreated            EventType = "created"
	EventTypeUpdated            EventType = "updated"
	EventTypeCompleted          EventType = "completed"
	EventTypeCancelled          EventType = "cancelled"
	EventTypeRescheduled        EventType = "rescheduled"
	EventTypeRestored           EventType = "restored"
	EventTypeLinked             EventType = "linked"
	EventTypeConvertedFromInbox EventType = "converted_from_inbox"
)

// ValidEventTypes returns all valid event types.
func ValidEventTypes() []EventType {
	return []EventType{
		EventTypeCreated,
		EventTypeUpdated,
		EventTypeCompleted,
		EventTypeCancelled,
		EventTypeRescheduled,
		EventTypeRestored,
		EventTypeLinked,
		EventTypeConvertedFromInbox,
	}
}

// IsValid checks if the event type is valid.
func (e EventType) IsValid() bool {
	switch e {
	case EventTypeCreated, EventTypeUpdated, EventTypeCompleted,
		EventTypeCancelled, EventTypeRescheduled, EventTypeRestored,
		EventTypeLinked, EventTypeConvertedFromInbox:
		return true
	default:
		return false
	}
}

// String returns the string representation of the event type.
func (e EventType) String() string {
	return string(e)
}

// Actor represents who performed the action.
type Actor string

const (
	ActorUser      Actor = "user"
	ActorAssistant Actor = "assistant"
	ActorSystem    Actor = "system"
)

// ValidActors returns all valid actor values.
func ValidActors() []Actor {
	return []Actor{ActorUser, ActorAssistant, ActorSystem}
}

// IsValid checks if the actor is valid.
func (a Actor) IsValid() bool {
	switch a {
	case ActorUser, ActorAssistant, ActorSystem:
		return true
	default:
		return false
	}
}

// TaskEvent represents an append-only event in the task history.
type TaskEvent struct {
	ID        int64
	TaskID    int64
	Type      EventType
	Payload   json.RawMessage
	Actor     Actor
	CreatedAt time.Time // UTC
}

// NewTaskEvent creates a new task event with the current time.
func NewTaskEvent(taskID int64, eventType EventType, payload interface{}, actor Actor) (*TaskEvent, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &TaskEvent{
		TaskID:    taskID,
		Type:      eventType,
		Payload:   payloadBytes,
		Actor:     actor,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// PayloadAs unmarshals the payload into the provided target.
func (e *TaskEvent) PayloadAs(target interface{}) error {
	return json.Unmarshal(e.Payload, target)
}
