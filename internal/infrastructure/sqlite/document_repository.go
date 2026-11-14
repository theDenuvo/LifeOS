package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// DocumentUnitOfWork implements documents.UnitOfWork on top of SQLite.
type DocumentUnitOfWork struct {
	db *sql.DB
}

// NewDocumentUnitOfWork builds a document unit of work.
func NewDocumentUnitOfWork(db *sql.DB) *DocumentUnitOfWork {
	return &DocumentUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *DocumentUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx appdocuments.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, documentTransaction{tx: tx})
	})
}

type documentTransaction struct {
	tx *sql.Tx
}

func (t documentTransaction) Documents() appdocuments.Repository {
	return &documentRepository{tx: t.tx}
}

type documentRepository struct {
	tx *sql.Tx
}

const documentColumns = `id, filename, mime, size, sha256, path, status, error, extracted_text, created_at, updated_at`
const proposalColumns = `id, document_id, title, note, due_date, priority, confidence, source, status, task_id, created_at, updated_at`

// CreateDocument stores a new document.
func (r *documentRepository) CreateDocument(ctx context.Context, value *document.Document) (int64, error) {
	result, err := r.tx.ExecContext(ctx, `
		INSERT INTO documents (filename, mime, size, sha256, path, status, error, extracted_text, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.Filename,
		value.MIME,
		value.Size,
		value.SHA256,
		value.Path,
		string(value.Status),
		encodeStringPtr(value.Error),
		encodeStringPtr(value.ExtractedText),
		unixSeconds(value.CreatedAt),
		unixSeconds(value.UpdatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert document: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert document: read id: %w", err)
	}
	return id, nil
}

// GetDocument loads one document.
func (r *documentRepository) GetDocument(ctx context.Context, id int64) (*document.Document, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents WHERE id = ?`, id)
	value, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appdocuments.ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get document %d: %w", id, err)
	}
	return value, nil
}

// ListDocuments returns a page of documents.
func (r *documentRepository) ListDocuments(ctx context.Context, limit, offset int) ([]*document.Document, int, error) {
	var total int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+documentColumns+` FROM documents ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	values := make([]*document.Document, 0, limit)
	for rows.Next() {
		value, err := scanDocument(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return values, total, nil
}

// SetDocumentStatus updates processing state.
func (r *documentRepository) SetDocumentStatus(ctx context.Context, id int64, status document.Status, errorText *string, extractedText *string, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		UPDATE documents SET status = ?, error = ?, extracted_text = COALESCE(?, extracted_text), updated_at = ?
		WHERE id = ?`,
		string(status), encodeStringPtr(errorText), encodeStringPtr(extractedText), unixSeconds(at), id)
	if err != nil {
		return fmt.Errorf("set document status: %w", err)
	}
	return nil
}

// ReplaceProposals stores the extraction result and marks the document processed.
func (r *documentRepository) ReplaceProposals(ctx context.Context, documentID int64, proposals []*document.ProposedTask, extractedText string, at time.Time) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM document_tasks WHERE document_id = ?`, documentID); err != nil {
		return err
	}
	if _, err := r.tx.ExecContext(ctx, `
		UPDATE documents SET status = 'processed', error = NULL, extracted_text = ?, updated_at = ? WHERE id = ?`,
		extractedText, unixSeconds(at), documentID); err != nil {
		return err
	}
	for _, proposal := range proposals {
		_, err := r.tx.ExecContext(ctx, `
			INSERT INTO document_tasks
				(document_id, title, note, due_date, priority, confidence, source, status, task_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			proposal.DocumentID,
			proposal.Title,
			encodeStringPtr(proposal.Note),
			encodeCalendarDate(proposal.DueDate),
			string(proposal.Priority),
			proposal.Confidence,
			encodeStringPtr(proposal.Source),
			string(proposal.Status),
			encodeParentID(proposal.TaskID),
			unixSeconds(proposal.CreatedAt),
			unixSeconds(proposal.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert document task: %w", err)
		}
	}
	return nil
}

// ListProposals returns proposals for one document.
func (r *documentRepository) ListProposals(ctx context.Context, documentID int64) ([]*document.ProposedTask, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+proposalColumns+` FROM document_tasks WHERE document_id = ? ORDER BY id ASC`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*document.ProposedTask, 0)
	for rows.Next() {
		value, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// GetProposal loads one proposal.
func (r *documentRepository) GetProposal(ctx context.Context, id int64) (*document.ProposedTask, error) {
	row := r.tx.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM document_tasks WHERE id = ?`, id)
	value, err := scanProposal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appdocuments.ErrDocumentNotFound
	}
	return value, err
}

// MarkProposalAccepted marks a proposal as imported.
func (r *documentRepository) MarkProposalAccepted(ctx context.Context, id, taskID int64, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		UPDATE document_tasks SET status = 'accepted', task_id = ?, updated_at = ? WHERE id = ?`,
		taskID, unixSeconds(at), id)
	return err
}

// MarkProposalDismissed marks a proposal as dismissed.
func (r *documentRepository) MarkProposalDismissed(ctx context.Context, id int64, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		UPDATE document_tasks SET status = 'dismissed', updated_at = ? WHERE id = ?`,
		unixSeconds(at), id)
	return err
}

// DeleteDocument removes a document and its proposals.
func (r *documentRepository) DeleteDocument(ctx context.Context, id int64) error {
	_, err := r.tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	return err
}

func scanDocument(row rowScanner) (*document.Document, error) {
	var (
		id            int64
		filename      string
		mime          string
		size          int64
		sha           string
		path          string
		status        string
		errorText     sql.NullString
		extractedText sql.NullString
		createdAt     int64
		updatedAt     int64
	)
	if err := row.Scan(&id, &filename, &mime, &size, &sha, &path, &status, &errorText, &extractedText, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	value := &document.Document{
		ID:            id,
		Filename:      filename,
		MIME:          mime,
		Size:          size,
		SHA256:        sha,
		Path:          path,
		Status:        document.Status(status),
		Error:         stringPtr(errorText),
		ExtractedText: stringPtr(extractedText),
		CreatedAt:     timeFromUnix(createdAt),
		UpdatedAt:     timeFromUnix(updatedAt),
	}
	if !value.Status.IsValid() {
		return nil, fmt.Errorf("document %d: invalid status %q", id, status)
	}
	return value, nil
}

func scanProposal(row rowScanner) (*document.ProposedTask, error) {
	var (
		id         int64
		documentID int64
		title      string
		note       sql.NullString
		dueDate    any
		priority   string
		confidence float64
		source     sql.NullString
		status     string
		taskID     sql.NullInt64
		createdAt  int64
		updatedAt  int64
	)
	if err := row.Scan(&id, &documentID, &title, &note, &dueDate, &priority, &confidence, &source, &status, &taskID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	due, err := decodeCalendarDate(dueDate)
	if err != nil {
		return nil, fmt.Errorf("document task %d: invalid due date: %w", id, err)
	}
	value := &document.ProposedTask{
		ID:         id,
		DocumentID: documentID,
		Title:      title,
		Note:       stringPtr(note),
		DueDate:    due,
		Priority:   task.Priority(priority),
		Confidence: confidence,
		Source:     stringPtr(source),
		Status:     document.TaskStatus(status),
		CreatedAt:  timeFromUnix(createdAt),
		UpdatedAt:  timeFromUnix(updatedAt),
	}
	if taskID.Valid {
		value.TaskID = &taskID.Int64
	}
	if !value.Status.IsValid() {
		return nil, fmt.Errorf("document task %d: invalid status %q", id, status)
	}
	return value, nil
}
