package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// Application errors.
var (
	// ErrConversationNotFound is returned when a conversation does not exist.
	ErrConversationNotFound = errors.New("conversation not found")

	// ErrPendingActionNotFound is returned when a pending action does not exist.
	ErrPendingActionNotFound = errors.New("pending action not found")

	// ErrPendingActionResolved is returned when a pending action is no longer pending.
	ErrPendingActionResolved = errors.New("pending action already resolved")

	// ErrProviderNotConfigured is returned when no LLM provider is available.
	ErrProviderNotConfigured = errors.New("assistant provider is not configured")

	// ErrToolNotFound is returned when the model calls an unknown tool.
	ErrToolNotFound = errors.New("tool not found")

	// ErrToolFailed wraps a tool execution failure.
	ErrToolFailed = errors.New("tool execution failed")
)

// ToolSpec describes a tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one tool invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// InteractionContext describes where the user is and what is selected.
//
// It is advisory context, not evidence: the model may use it to resolve
// references such as "this task", but must still read authoritative data with
// tools.
type InteractionContext struct {
	Screen     string
	ObjectType string
	ObjectID   string
	Title      string
	Documents  []DocumentRef
}

// DocumentRef is an uploaded document attached to the current message.
type DocumentRef struct {
	ID            int64
	Filename      string
	Size          int64
	MarkdownChars int
}

// DocumentText is the extracted Markdown of one attached document.
type DocumentText struct {
	ID       int64
	Filename string
	Text     string
}

// DocumentTextProvider loads extracted document text for prompt injection.
//
// Every model must receive the attached document itself, not just its numeric
// ID: requesting an ID from the user is a failure of the interface.
type DocumentTextProvider interface {
	DocumentText(ctx context.Context, id int64) (DocumentText, error)
}

// ChatMessage is one provider-facing message.
type ChatMessage struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Completion is one model response.
type Completion struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCall
}

// StreamDelta is one incremental token chunk.
type StreamDelta struct {
	Content   string
	Reasoning string
}

// StreamingProvider is implemented by providers that can stream tokens.
type StreamingProvider interface {
	StreamComplete(ctx context.Context, messages []ChatMessage, tools []ToolSpec, onDelta func(StreamDelta) error) (Completion, error)
}

// ModelAwareProvider can produce a provider bound to another model.
type ModelAwareProvider interface {
	WithModel(model string) Provider
}

// ProviderRetry describes one retry or model fallback inside the runtime.
type ProviderRetry struct {
	Model    string
	Attempt  int
	Fallback bool
	Reason   string
}

// RetryObserverProvider accepts a callback for retry and fallback events, so
// the workspace can say "retrying" instead of showing a dead turn.
type RetryObserverProvider interface {
	WithRetryObserver(observer func(ProviderRetry)) Provider
}

// Provider is the LLM boundary. Implementations live in internal/agent.
type Provider interface {
	Complete(ctx context.Context, messages []ChatMessage, tools []ToolSpec) (Completion, error)
}

// ToolRunner exposes the LifeOS tools to the assistant.
type ToolRunner interface {
	Specs() []ToolSpec
	IsReadOnly(name string) bool
	Execute(ctx context.Context, name string, arguments json.RawMessage) (any, error)
	Preview(name string, arguments json.RawMessage) (string, error)
}

// Repository persists assistant state.
type Repository interface {
	CreateConversation(ctx context.Context, conversation *domainassistant.Conversation) (int64, error)
	GetConversation(ctx context.Context, id int64) (*domainassistant.Conversation, error)
	ListConversations(ctx context.Context, limit, offset int) ([]*domainassistant.Conversation, int, error)
	TouchConversation(ctx context.Context, id int64, at time.Time) error

	InsertMessage(ctx context.Context, message *domainassistant.Message) (int64, error)
	ListMessages(ctx context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error)

	InsertPendingAction(ctx context.Context, action *domainassistant.PendingAction) (int64, error)
	GetPendingAction(ctx context.Context, id int64) (*domainassistant.PendingAction, error)
	ListPendingActions(ctx context.Context, conversationID int64, status *domainassistant.PendingStatus) ([]*domainassistant.PendingAction, error)
	ResolvePendingAction(ctx context.Context, id int64, status domainassistant.PendingStatus, resolution string, at time.Time) error
}

// Transaction exposes the assistant repository bound to one transaction.
type Transaction interface {
	Assistant() Repository
}

// UnitOfWork defines the transaction boundary for assistant use cases.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}
