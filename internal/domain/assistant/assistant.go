// Package assistant owns conversations, messages and pending actions.
//
// A pending action is the safety boundary between the model and the user's
// data: the model may propose a mutation, but nothing is written until the user
// confirms it.
package assistant

import (
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// Validation errors.
var (
	// ErrInvalidContent is returned when a message body is empty or too long.
	ErrInvalidContent = errors.New("content is required and must be 1-8000 characters")

	// ErrInvalidRole is returned for an unknown message role.
	ErrInvalidRole = errors.New("role must be user, assistant or tool")

	// ErrInvalidPendingStatus is returned for an unknown pending-action status.
	ErrInvalidPendingStatus = errors.New("status must be pending, confirmed, rejected or expired")
)

// Role is the author of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// IsValid reports whether the role is known.
func (r Role) IsValid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// PendingStatus is the lifecycle state of a proposed action.
type PendingStatus string

const (
	PendingStatusPending   PendingStatus = "pending"
	PendingStatusConfirmed PendingStatus = "confirmed"
	PendingStatusRejected  PendingStatus = "rejected"
	PendingStatusExpired   PendingStatus = "expired"
)

// IsValid reports whether the status is known.
func (s PendingStatus) IsValid() bool {
	switch s {
	case PendingStatusPending, PendingStatusConfirmed, PendingStatusRejected, PendingStatusExpired:
		return true
	default:
		return false
	}
}

// Conversation is one assistant dialogue.
type Conversation struct {
	ID            int64
	Title         *string
	ContextScreen *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EndedAt       *time.Time
}

// Message is one turn in a conversation.
type Message struct {
	ID             int64
	ConversationID int64
	Role           Role
	Content        string
	ToolCalls      json.RawMessage
	ToolResults    json.RawMessage
	Metadata       json.RawMessage
	CreatedAt      time.Time
}

// PendingAction is a proposed mutation awaiting confirmation.
type PendingAction struct {
	ID             int64
	ConversationID int64
	MessageID      int64
	ToolName       string
	Arguments      json.RawMessage
	Preview        json.RawMessage
	Status         PendingStatus
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	Resolution     *string
}

// NewConversation builds a conversation with a title derived from the first
// user message.
func NewConversation(title string, now time.Time) *Conversation {
	conversation := &Conversation{
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
	if title != "" {
		conversation.Title = &title
	}
	return conversation
}

// NewMessage validates and builds a message.
func NewMessage(conversationID int64, role Role, content string, now time.Time) (*Message, error) {
	if !role.IsValid() {
		return nil, ErrInvalidRole
	}
	if role != RoleTool {
		if !utf8.ValidString(content) {
			return nil, ErrInvalidContent
		}
		length := utf8.RuneCountInString(content)
		// An assistant turn that only requests tools has no text yet; a user
		// message must always carry content.
		if length > 8000 || (length == 0 && role == RoleUser) {
			return nil, ErrInvalidContent
		}
	}
	return &Message{
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      now.UTC(),
	}, nil
}

// NewPendingAction validates and builds a proposed action.
func NewPendingAction(conversationID, messageID int64, toolName string, arguments, preview json.RawMessage, now time.Time) (*PendingAction, error) {
	if toolName == "" {
		return nil, errors.New("tool name is required")
	}
	if len(arguments) == 0 || !json.Valid(arguments) {
		return nil, errors.New("arguments must be valid JSON")
	}
	if len(preview) == 0 || !json.Valid(preview) {
		return nil, errors.New("preview must be valid JSON")
	}
	return &PendingAction{
		ConversationID: conversationID,
		MessageID:      messageID,
		ToolName:       toolName,
		Arguments:      arguments,
		Preview:        preview,
		Status:         PendingStatusPending,
		CreatedAt:      now.UTC(),
	}, nil
}

// Resolve moves a pending action to a terminal state.
func (p *PendingAction) Resolve(status PendingStatus, now time.Time) error {
	if !status.IsValid() || status == PendingStatusPending {
		return ErrInvalidPendingStatus
	}
	resolvedAt := now.UTC()
	resolution := string(status)
	p.Status = status
	p.ResolvedAt = &resolvedAt
	p.Resolution = &resolution
	return nil
}
