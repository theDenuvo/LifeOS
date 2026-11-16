package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// outreachRepository is the SQLite implementation of proactive.OutreachRepository.
type outreachRepository struct {
	tx *sql.Tx
}

// Create persists one self-initiated contact.
func (r *outreachRepository) Create(ctx context.Context, outreach *proactive.Outreach) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO outreach (conversation_id, message_id, insight_key, kind, severity,
		                      title, reason, proposal, outcome, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outreach.ConversationID, outreach.MessageID, outreach.InsightKey, outreach.Kind,
		string(outreach.Severity), outreach.Title, outreach.Reason, outreach.Proposal,
		string(outreach.Outcome), unixSeconds(outreach.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("create outreach: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("outreach id: %w", err)
	}
	return id, nil
}

// Get returns one outreach or nil when it does not exist.
func (r *outreachRepository) Get(ctx context.Context, id int64) (*proactive.Outreach, error) {
	row := r.tx.QueryRowContext(ctx, outreachSelect+"WHERE id = ?", id)
	outreach, err := scanOutreach(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get outreach %d: %w", id, err)
	}
	return outreach, nil
}

// Recent returns outreaches created after the given instant, oldest first.
func (r *outreachRepository) Recent(ctx context.Context, since time.Time) ([]proactive.Outreach, error) {
	rows, err := r.tx.QueryContext(ctx, outreachSelect+"WHERE created_at >= ? ORDER BY created_at ASC, id ASC", unixSeconds(since))
	if err != nil {
		return nil, fmt.Errorf("list recent outreach: %w", err)
	}
	defer rows.Close()
	return collectOutreach(rows)
}

// SetOutcome records the user's reaction.
func (r *outreachRepository) SetOutcome(ctx context.Context, id int64, outcome proactive.OutreachOutcome, decidedAt time.Time) error {
	result, err := r.tx.ExecContext(ctx,
		`UPDATE outreach SET outcome = ?, decided_at = ? WHERE id = ? AND outcome = 'pending'`,
		string(outcome), unixSeconds(decidedAt), id)
	if err != nil {
		return fmt.Errorf("update outreach %d: %w", id, err)
	}
	if count, err := result.RowsAffected(); err == nil && count == 0 {
		// The outreach is either missing or already decided; Get() distinguishes.
		return nil
	}
	return nil
}

// Snooze records a "not now" until the given instant.
func (r *outreachRepository) Snooze(ctx context.Context, id int64, until, decidedAt time.Time) error {
	_, err := r.tx.ExecContext(ctx,
		`UPDATE outreach SET outcome = 'snoozed', decided_at = ?, snoozed_until = ? WHERE id = ?`,
		unixSeconds(decidedAt), unixSeconds(until), id)
	if err != nil {
		return fmt.Errorf("snooze outreach %d: %w", id, err)
	}
	return nil
}

// List returns a page of outreaches, newest first.
func (r *outreachRepository) List(ctx context.Context, limit, offset int) ([]proactive.Outreach, int, error) {
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outreach`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count outreach: %w", err)
	}
	rows, err := r.tx.QueryContext(ctx, outreachSelect+"ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list outreach: %w", err)
	}
	defer rows.Close()
	found, err := collectOutreach(rows)
	if err != nil {
		return nil, 0, err
	}
	return found, total, nil
}

const outreachSelect = `
	SELECT id, conversation_id, message_id, insight_key, kind, severity,
	       title, reason, proposal, outcome, created_at, decided_at, snoozed_until
	FROM outreach
`

type outreachScanner interface {
	Scan(dest ...any) error
}

func scanOutreach(row outreachScanner) (*proactive.Outreach, error) {
	var (
		outreach     proactive.Outreach
		severity     string
		outcome      string
		createdAt    int64
		decidedAt    sql.NullInt64
		snoozedUntil sql.NullInt64
	)
	if err := row.Scan(&outreach.ID, &outreach.ConversationID, &outreach.MessageID,
		&outreach.InsightKey, &outreach.Kind, &severity, &outreach.Title,
		&outreach.Reason, &outreach.Proposal, &outcome, &createdAt, &decidedAt, &snoozedUntil); err != nil {
		return nil, err
	}
	outreach.Severity = proactive.Severity(severity)
	outreach.Outcome = proactive.OutreachOutcome(outcome)
	outreach.CreatedAt = timeFromUnix(createdAt)
	if decidedAt.Valid {
		value := timeFromUnix(decidedAt.Int64)
		outreach.DecidedAt = &value
	}
	if snoozedUntil.Valid {
		value := timeFromUnix(snoozedUntil.Int64)
		outreach.SnoozedUntil = &value
	}
	return &outreach, nil
}

func collectOutreach(rows *sql.Rows) ([]proactive.Outreach, error) {
	outreach := make([]proactive.Outreach, 0, 8)
	for rows.Next() {
		item, err := scanOutreach(rows)
		if err != nil {
			return nil, fmt.Errorf("scan outreach: %w", err)
		}
		outreach = append(outreach, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outreach: %w", err)
	}
	return outreach, nil
}

// OutreachAssistant adapts the assistant unit of work to the initiative
// engine's narrow surface. The adapter lives here because the assistant
// application package already imports proactive, so the engine itself cannot
// hold the full unit of work type.
type OutreachAssistant struct {
	db *sql.DB
}

// NewOutreachAssistant builds the adapter.
func NewOutreachAssistant(db *sql.DB) *OutreachAssistant {
	return &OutreachAssistant{db: db}
}

// Do runs fn inside one transaction bound to the assistant repository and the
// outreach repository: the chat message and its history entry commit together
// or not at all.
func (a *OutreachAssistant) Do(ctx context.Context, fn func(ctx context.Context, tx proactive.AssistantTx) error) error {
	return runInTransaction(ctx, a.db, func(tx *sql.Tx) error {
		return fn(ctx, outreachAssistantTx{
			assistant: &assistantRepository{tx: tx},
			outreach:  &outreachRepository{tx: tx},
		})
	})
}

// outreachAssistantTx exposes both repositories to one transaction.
type outreachAssistantTx struct {
	assistant *assistantRepository
	outreach  *outreachRepository
}

func (t outreachAssistantTx) CreateConversation(ctx context.Context, conversation *domainassistant.Conversation) (int64, error) {
	return t.assistant.CreateConversation(ctx, conversation)
}

func (t outreachAssistantTx) GetConversation(ctx context.Context, id int64) (*domainassistant.Conversation, error) {
	return t.assistant.GetConversation(ctx, id)
}

func (t outreachAssistantTx) ListConversations(ctx context.Context, limit, offset int, kind *domainassistant.Kind) ([]*domainassistant.Conversation, int, error) {
	return t.assistant.ListConversations(ctx, limit, offset, kind)
}

func (t outreachAssistantTx) TouchConversation(ctx context.Context, id int64, at time.Time) error {
	return t.assistant.TouchConversation(ctx, id, at)
}

func (t outreachAssistantTx) InsertMessage(ctx context.Context, message *domainassistant.Message) (int64, error) {
	return t.assistant.InsertMessage(ctx, message)
}

func (t outreachAssistantTx) ListMessages(ctx context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error) {
	return t.assistant.ListMessages(ctx, conversationID, limit)
}

func (t outreachAssistantTx) CreateOutreach(ctx context.Context, outreach *proactive.Outreach) (int64, error) {
	return t.outreach.Create(ctx, outreach)
}
