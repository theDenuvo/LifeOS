package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	domaininbox "github.com/theDenuvo/LifeOS/internal/domain/inbox"
)

// InboxUnitOfWork implements inbox.UnitOfWork on top of SQLite.
//
// It shares the transaction runner with the task unit of work, so conversion can
// write the task, its event and the processed inbox item atomically.
type InboxUnitOfWork struct {
	db *sql.DB
}

// NewInboxUnitOfWork builds an inbox unit of work over an open database.
func NewInboxUnitOfWork(db *sql.DB) *InboxUnitOfWork {
	return &InboxUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *InboxUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx inbox.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, taskTransaction{tx: tx})
	})
}

// inboxRepository is the SQLite implementation of inbox.Repository.
type inboxRepository struct {
	tx *sql.Tx
}

const inboxColumns = `id, text, meta, created_at, processed_at, converted_task_id`

// FindByID loads one inbox item.
func (r *inboxRepository) FindByID(ctx context.Context, id int64) (*domaininbox.Item, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM inbox_items WHERE id = ?`, id)
	item, err := scanInboxItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, inbox.ErrItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find inbox item %d: %w", id, err)
	}
	return item, nil
}

// List returns a page of inbox items plus the total count.
func (r *inboxRepository) List(ctx context.Context, filter inbox.Filter) (inbox.ListResult, error) {
	where := ""
	if filter.UnprocessedOnly {
		where = " WHERE processed_at IS NULL"
	}

	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_items`+where).Scan(&total); err != nil {
		return inbox.ListResult{}, fmt.Errorf("count inbox items: %w", err)
	}

	limit := filter.Limit.Or(inbox.DefaultPageLimit)
	offset := filter.Offset.Or(0)

	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+inboxColumns+` FROM inbox_items`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return inbox.ListResult{}, fmt.Errorf("list inbox items: %w", err)
	}
	defer rows.Close()

	items := make([]*domaininbox.Item, 0, limit)
	for rows.Next() {
		item, err := scanInboxItem(rows)
		if err != nil {
			return inbox.ListResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return inbox.ListResult{}, fmt.Errorf("scan inbox items: %w", err)
	}

	return inbox.ListResult{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// Insert stores a new inbox item and returns its ID.
func (r *inboxRepository) Insert(ctx context.Context, item *domaininbox.Item) (int64, error) {
	if err := item.Validate(); err != nil {
		return 0, err
	}

	var meta any
	if len(item.Meta) > 0 {
		meta = string(item.Meta)
	}

	result, err := r.tx.ExecContext(ctx,
		`INSERT INTO inbox_items (text, meta, created_at) VALUES (?, ?, ?)`,
		item.Text, meta, unixSeconds(item.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert inbox item: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert inbox item: read id: %w", err)
	}
	return id, nil
}

// MarkProcessed records the conversion and links the created task.
func (r *inboxRepository) MarkProcessed(ctx context.Context, id int64, processedAt time.Time, taskID int64) error {
	result, err := r.tx.ExecContext(ctx,
		`UPDATE inbox_items SET processed_at = ?, converted_task_id = ? WHERE id = ? AND processed_at IS NULL`,
		unixSeconds(processedAt), taskID, id)
	if err != nil {
		return fmt.Errorf("mark inbox item %d processed: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark inbox item %d processed: rows affected: %w", id, err)
	}
	if affected == 0 {
		return inbox.ErrAlreadyProcessed
	}
	return nil
}

// Delete removes an inbox item.
func (r *inboxRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.tx.ExecContext(ctx, `DELETE FROM inbox_items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete inbox item %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete inbox item %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		return inbox.ErrItemNotFound
	}
	return nil
}

// CountUnprocessed returns the number of items awaiting triage.
func (r *inboxRepository) CountUnprocessed(ctx context.Context) (int, error) {
	var count int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_items WHERE processed_at IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unprocessed inbox items: %w", err)
	}
	return count, nil
}

// Search looks for text in captured items.
//
// Matching happens in Go with Unicode-aware case folding: SQLite's lower()/LIKE
// only fold ASCII, which would miss "Молоко" for a "молоко" query. The personal
// inbox is small enough that scanning it is cheaper than maintaining a separate
// search index.
func (r *inboxRepository) Search(ctx context.Context, query string, limit int) ([]*domaininbox.Item, int, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+inboxColumns+` FROM inbox_items ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, 0, fmt.Errorf("search inbox: %w", err)
	}
	defer rows.Close()

	needle := strings.ToLower(query)
	matches := make([]*domaininbox.Item, 0, limit)
	total := 0
	for rows.Next() {
		item, err := scanInboxItem(rows)
		if err != nil {
			return nil, 0, err
		}
		if !strings.Contains(strings.ToLower(item.Text), needle) {
			continue
		}
		total++
		if len(matches) < limit {
			matches = append(matches, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan inbox search: %w", err)
	}
	return matches, total, nil
}

// scanInboxItem converts one row into the domain entity.
func scanInboxItem(row rowScanner) (*domaininbox.Item, error) {
	var (
		id              int64
		text            string
		meta            sql.NullString
		createdAt       int64
		processedAt     sql.NullInt64
		convertedTaskID sql.NullInt64
	)
	if err := row.Scan(&id, &text, &meta, &createdAt, &processedAt, &convertedTaskID); err != nil {
		return nil, err
	}

	item := &domaininbox.Item{
		ID:        id,
		Text:      text,
		CreatedAt: timeFromUnix(createdAt),
	}
	if meta.Valid && meta.String != "" {
		item.Meta = json.RawMessage(meta.String)
	}
	if processedAt.Valid {
		value := timeFromUnix(processedAt.Int64)
		item.ProcessedAt = &value
	}
	if convertedTaskID.Valid {
		value := convertedTaskID.Int64
		item.ConvertedTaskID = &value
	}

	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("inbox item %d: invalid stored row: %w", id, err)
	}
	return item, nil
}
