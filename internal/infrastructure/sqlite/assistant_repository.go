package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// AssistantUnitOfWork implements assistant.UnitOfWork on top of SQLite.
type AssistantUnitOfWork struct {
	db *sql.DB
}

// NewAssistantUnitOfWork builds an assistant unit of work.
func NewAssistantUnitOfWork(db *sql.DB) *AssistantUnitOfWork {
	return &AssistantUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *AssistantUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx assistant.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, taskTransaction{tx: tx})
	})
}

// assistantRepository is the SQLite implementation of assistant.Repository.
type assistantRepository struct {
	tx *sql.Tx
}

const conversationColumns = `id, title, context_screen, created_at, updated_at, ended_at`
const messageColumns = `id, conversation_id, role, content, tool_calls, tool_results, metadata, created_at`
const pendingColumns = `id, conversation_id, message_id, tool_name, arguments, preview, status, created_at, resolved_at, resolution`

// CreateConversation stores a conversation and returns its ID.
func (r *assistantRepository) CreateConversation(ctx context.Context, conversation *domainassistant.Conversation) (int64, error) {
	result, err := r.tx.ExecContext(ctx,
		`INSERT INTO conversations (title, context_screen, created_at, updated_at, ended_at) VALUES (?, ?, ?, ?, ?)`,
		encodeStringPtr(conversation.Title),
		encodeStringPtr(conversation.ContextScreen),
		unixSeconds(conversation.CreatedAt),
		unixSeconds(conversation.UpdatedAt),
		encodeTimePtr(conversation.EndedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert conversation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert conversation: read id: %w", err)
	}
	return id, nil
}

// GetConversation loads one conversation.
func (r *assistantRepository) GetConversation(ctx context.Context, id int64) (*domainassistant.Conversation, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE id = ?`, id)
	conversation, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, assistant.ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation %d: %w", id, err)
	}
	return conversation, nil
}

// ListConversations returns a page of conversations plus the total count.
func (r *assistantRepository) ListConversations(ctx context.Context, limit, offset int) ([]*domainassistant.Conversation, int, error) {
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count conversations: %w", err)
	}

	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+conversationColumns+` FROM conversations ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	conversations := make([]*domainassistant.Conversation, 0, limit)
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, 0, err
		}
		conversations = append(conversations, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan conversations: %w", err)
	}
	return conversations, total, nil
}

// TouchConversation updates the conversation's updated_at.
func (r *assistantRepository) TouchConversation(ctx context.Context, id int64, at time.Time) error {
	if _, err := r.tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, unixSeconds(at), id); err != nil {
		return fmt.Errorf("touch conversation %d: %w", id, err)
	}
	return nil
}

// InsertMessage stores a message and returns its ID.
func (r *assistantRepository) InsertMessage(ctx context.Context, message *domainassistant.Message) (int64, error) {
	result, err := r.tx.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, tool_calls, tool_results, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		message.ConversationID,
		string(message.Role),
		message.Content,
		encodeRaw(message.ToolCalls),
		encodeRaw(message.ToolResults),
		encodeRaw(message.Metadata),
		unixSeconds(message.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert message: read id: %w", err)
	}
	return id, nil
}

// ListMessages returns the most recent messages in chronological order.
func (r *assistantRepository) ListMessages(ctx context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM (
			SELECT `+messageColumns+` FROM messages WHERE conversation_id = ?
			ORDER BY created_at DESC, id DESC LIMIT ?
		) ORDER BY created_at ASC, id ASC`,
		conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]*domainassistant.Message, 0, limit)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan messages: %w", err)
	}
	return messages, nil
}

// InsertPendingAction stores a proposed mutation.
func (r *assistantRepository) InsertPendingAction(ctx context.Context, action *domainassistant.PendingAction) (int64, error) {
	result, err := r.tx.ExecContext(ctx,
		`INSERT INTO pending_actions (conversation_id, message_id, tool_name, arguments, preview, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		action.ConversationID,
		action.MessageID,
		action.ToolName,
		string(action.Arguments),
		string(action.Preview),
		string(action.Status),
		unixSeconds(action.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert pending action: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert pending action: read id: %w", err)
	}
	return id, nil
}

// GetPendingAction loads one pending action.
func (r *assistantRepository) GetPendingAction(ctx context.Context, id int64) (*domainassistant.PendingAction, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM pending_actions WHERE id = ?`, id)
	action, err := scanPendingAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, assistant.ErrPendingActionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get pending action %d: %w", id, err)
	}
	return action, nil
}

// ListPendingActions returns pending actions for a conversation.
func (r *assistantRepository) ListPendingActions(ctx context.Context, conversationID int64, status *domainassistant.PendingStatus) ([]*domainassistant.PendingAction, error) {
	query := `SELECT ` + pendingColumns + ` FROM pending_actions WHERE conversation_id = ?`
	args := []any{conversationID}
	if status != nil {
		query += ` AND status = ?`
		args = append(args, string(*status))
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pending actions: %w", err)
	}
	defer rows.Close()

	actions := make([]*domainassistant.PendingAction, 0, 8)
	for rows.Next() {
		action, err := scanPendingAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan pending actions: %w", err)
	}
	return actions, nil
}

// ResolvePendingAction moves a pending action to a terminal state.
func (r *assistantRepository) ResolvePendingAction(ctx context.Context, id int64, status domainassistant.PendingStatus, resolution string, at time.Time) error {
	result, err := r.tx.ExecContext(ctx,
		`UPDATE pending_actions SET status = ?, resolution = ?, resolved_at = ? WHERE id = ? AND status = 'pending'`,
		string(status), resolution, unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("resolve pending action %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve pending action %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		return assistant.ErrPendingActionResolved
	}
	return nil
}

func scanConversation(row rowScanner) (*domainassistant.Conversation, error) {
	var (
		id            int64
		title         sql.NullString
		contextScreen sql.NullString
		createdAt     int64
		updatedAt     int64
		endedAt       sql.NullInt64
	)
	if err := row.Scan(&id, &title, &contextScreen, &createdAt, &updatedAt, &endedAt); err != nil {
		return nil, err
	}
	return &domainassistant.Conversation{
		ID:            id,
		Title:         stringPtr(title),
		ContextScreen: stringPtr(contextScreen),
		CreatedAt:     timeFromUnix(createdAt),
		UpdatedAt:     timeFromUnix(updatedAt),
		EndedAt:       timePtr(endedAt),
	}, nil
}

func scanMessage(row rowScanner) (*domainassistant.Message, error) {
	var (
		id             int64
		conversationID int64
		role           string
		content        string
		toolCalls      sql.NullString
		toolResults    sql.NullString
		metadata       sql.NullString
		createdAt      int64
	)
	if err := row.Scan(&id, &conversationID, &role, &content, &toolCalls, &toolResults, &metadata, &createdAt); err != nil {
		return nil, err
	}
	message := &domainassistant.Message{
		ID:             id,
		ConversationID: conversationID,
		Role:           domainassistant.Role(role),
		Content:        content,
		ToolCalls:      rawPtr(toolCalls),
		ToolResults:    rawPtr(toolResults),
		Metadata:       rawPtr(metadata),
		CreatedAt:      timeFromUnix(createdAt),
	}
	if !message.Role.IsValid() {
		return nil, fmt.Errorf("message %d: invalid stored role %q", id, role)
	}
	return message, nil
}

func scanPendingAction(row rowScanner) (*domainassistant.PendingAction, error) {
	var (
		id             int64
		conversationID int64
		messageID      int64
		toolName       string
		arguments      string
		preview        string
		status         string
		createdAt      int64
		resolvedAt     sql.NullInt64
		resolution     sql.NullString
	)
	if err := row.Scan(&id, &conversationID, &messageID, &toolName, &arguments, &preview, &status, &createdAt, &resolvedAt, &resolution); err != nil {
		return nil, err
	}
	action := &domainassistant.PendingAction{
		ID:             id,
		ConversationID: conversationID,
		MessageID:      messageID,
		ToolName:       toolName,
		Arguments:      json.RawMessage(arguments),
		Preview:        json.RawMessage(preview),
		Status:         domainassistant.PendingStatus(status),
		CreatedAt:      timeFromUnix(createdAt),
		ResolvedAt:     timePtr(resolvedAt),
		Resolution:     stringPtr(resolution),
	}
	if !action.Status.IsValid() {
		return nil, fmt.Errorf("pending action %d: invalid stored status %q", id, status)
	}
	return action, nil
}

func encodeRaw(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func rawPtr(value sql.NullString) json.RawMessage {
	if !value.Valid || value.String == "" {
		return nil
	}
	return json.RawMessage(value.String)
}
