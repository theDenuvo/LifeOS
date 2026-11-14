package documents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// MaxUploadBytes is the hard upload limit for a source document.
const MaxUploadBytes int64 = 25 << 20

// AcceptResult is the outcome of importing proposals.
type AcceptResult struct {
	Tasks     []tasks.TaskWithVersion
	Proposals []*document.ProposedTask
}

// Service implements document upload, extraction and proposal acceptance.
type Service struct {
	uow               UnitOfWork
	store             FileStore
	extractor         Extractor
	markdownConverter MarkdownConverter
	taskCreator       TaskCreator
	clock             Clock
	timezone          string
}

// Option customises the service.
type Option func(*Service)

// NewService builds the document service.
func NewService(uow UnitOfWork, store FileStore, extractor Extractor, markdownConverter MarkdownConverter, taskCreator TaskCreator, clock Clock, timezone string, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	s := &Service{
		uow:               uow,
		store:             store,
		extractor:         extractor,
		markdownConverter: markdownConverter,
		taskCreator:       taskCreator,
		clock:             clock,
		timezone:          timezone,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Upload persists a source file and creates a pending document record.
func (s *Service) Upload(ctx context.Context, filename, mime string, reader io.Reader, size int64) (*document.Document, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "document"
	}
	if size > MaxUploadBytes {
		return nil, ErrFileTooLarge
	}
	stored, err := s.store.Save(filename, reader, MaxUploadBytes)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	value := &document.Document{
		Filename:  filename,
		MIME:      mime,
		Size:      stored.Size,
		SHA256:    stored.SHA256,
		Path:      stored.Path,
		Status:    document.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		id, err := tx.Documents().CreateDocument(ctx, value)
		if err != nil {
			return err
		}
		value.ID = id
		return nil
	})
	if err != nil {
		_ = s.store.Delete(stored.Path)
		return nil, err
	}
	return value, nil
}

// Process extracts text and tasks for one document.
func (s *Service) Process(ctx context.Context, id int64) error {
	value, err := s.getDocument(ctx, id)
	if err != nil {
		return err
	}
	if value.Status == document.StatusProcessed {
		return nil
	}
	now := s.clock.Now()
	if err := s.setStatus(ctx, id, document.StatusProcessing, nil, nil, now); err != nil {
		return err
	}

	fail := func(cause error) error {
		message := cause.Error()
		_ = s.setStatus(context.Background(), id, document.StatusFailed, &message, nil, s.clock.Now())
		return cause
	}

	if s.extractor == nil {
		return fail(errors.New("document extractor is not configured"))
	}
	extraction, err := s.extractor.Extract(ctx, value.Path, value.Filename, value.MIME)
	if err != nil {
		return fail(err)
	}
	markdown := strings.TrimSpace(extraction.Text)
	if len(extraction.Image) > 0 {
		if s.markdownConverter == nil {
			return fail(ErrLLMNotConfigured)
		}
		converted, err := s.markdownConverter.ConvertToMarkdown(ctx, TaskExtractionInput{
			Filename:  value.Filename,
			Image:     extraction.Image,
			ImageMIME: extraction.ImageMIME,
			Today:     now,
			Timezone:  s.timezone,
		})
		if err != nil {
			return fail(err)
		}
		markdown = strings.TrimSpace(converted)
	}
	if markdown == "" {
		return fail(errors.New("в документе не найден текст"))
	}
	if err := s.setStatus(ctx, id, document.StatusProcessed, nil, &markdown, s.clock.Now()); err != nil {
		return fail(err)
	}
	return nil
}

// Retry moves a failed document back to pending so it can be processed again.
func (s *Service) Retry(ctx context.Context, id int64) error {
	if _, err := s.getDocument(ctx, id); err != nil {
		return err
	}
	return s.setStatus(ctx, id, document.StatusPending, nil, nil, s.clock.Now())
}

// Get returns a document with its task proposals.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	var result View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		value, err := tx.Documents().GetDocument(ctx, id)
		if err != nil {
			return err
		}
		proposals, err := tx.Documents().ListProposals(ctx, id)
		if err != nil {
			return err
		}
		result = View{Document: value, Tasks: proposals}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return result, nil
}

// List returns a page of documents.
func (s *Service) List(ctx context.Context, limit, offset int) ([]*document.Document, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var (
		documents []*document.Document
		total     int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, count, err := tx.Documents().ListDocuments(ctx, limit, offset)
		if err != nil {
			return err
		}
		documents = found
		total = count
		return nil
	})
	return documents, total, err
}

// Accept imports selected proposals as real tasks.
func (s *Service) Accept(ctx context.Context, documentID int64, proposalIDs []int64) (AcceptResult, error) {
	if s.taskCreator == nil {
		return AcceptResult{}, errors.New("task service is not configured")
	}
	value, err := s.getDocument(ctx, documentID)
	if err != nil {
		return AcceptResult{}, err
	}
	var proposals []*document.ProposedTask
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Documents().ListProposals(ctx, documentID)
		if err != nil {
			return err
		}
		proposals = found
		return nil
	})
	if err != nil {
		return AcceptResult{}, err
	}
	selected := make(map[int64]struct{}, len(proposalIDs))
	for _, id := range proposalIDs {
		selected[id] = struct{}{}
	}
	now := s.clock.Now()
	result := AcceptResult{Proposals: proposals}
	for _, proposal := range proposals {
		if proposal.Status != document.TaskProposed {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[proposal.ID]; !ok {
				continue
			}
		}
		note := ""
		if proposal.Note != nil {
			note = *proposal.Note
		}
		if proposal.Source != nil {
			if note != "" {
				note += "\n\n"
			}
			note += "Источник: " + *proposal.Source
		}
		input := tasks.CreateTaskInput{Title: proposal.Title, Priority: tasks.Some(proposal.Priority)}
		if note != "" {
			input.Note = tasks.Some(note)
		}
		if proposal.DueDate != nil {
			input.DueDate = tasks.Some(*proposal.DueDate)
		}
		created, err := s.taskCreator.CreateTask(ctx, input, task.ActorUser)
		if err != nil {
			return result, fmt.Errorf("create task from document: %w", err)
		}
		err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
			return tx.Documents().MarkProposalAccepted(ctx, proposal.ID, created.Task.ID, now)
		})
		if err != nil {
			return result, err
		}
		result.Tasks = append(result.Tasks, created)
		proposal.Status = document.TaskAccepted
		proposal.TaskID = &created.Task.ID
	}
	if len(result.Tasks) == 0 {
		return result, ErrNoTasks
	}
	_ = value
	return result, nil
}

// Delete removes a document and its source file.
func (s *Service) Delete(ctx context.Context, id int64) error {
	value, err := s.getDocument(ctx, id)
	if err != nil {
		return err
	}
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Documents().DeleteDocument(ctx, id)
	})
	if err != nil {
		return err
	}
	if s.store != nil {
		_ = s.store.Delete(value.Path)
	}
	return nil
}

func (s *Service) getDocument(ctx context.Context, id int64) (*document.Document, error) {
	var value *document.Document
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Documents().GetDocument(ctx, id)
		if err != nil {
			return err
		}
		value = found
		return nil
	})
	return value, err
}

func (s *Service) setStatus(ctx context.Context, id int64, status document.Status, errorText, extractedText *string, at time.Time) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Documents().SetDocumentStatus(ctx, id, status, errorText, extractedText, at)
	})
}

func buildProposals(documentID int64, extracted []ExtractedTask, now time.Time) []*document.ProposedTask {
	result := make([]*document.ProposedTask, 0, len(extracted))
	seen := make(map[string]struct{}, len(extracted))
	for _, raw := range extracted {
		priority := task.Priority(strings.ToLower(strings.TrimSpace(raw.Priority)))
		if !priority.IsValid() {
			priority = task.PriorityMedium
		}
		confidence := raw.Confidence
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}
		var due *task.CalendarDate
		if raw.DueDate != nil && strings.TrimSpace(*raw.DueDate) != "" && strings.TrimSpace(*raw.DueDate) != "null" {
			if parsed, err := task.ParseCalendarDate(strings.TrimSpace(*raw.DueDate)); err == nil {
				due = &parsed
			}
		}
		note := raw.Note
		source := raw.Source
		proposal, err := document.NewProposedTask(documentID, raw.Title, note, due, priority, confidence, source, now)
		if err != nil {
			continue
		}
		key := strings.ToLower(proposal.Title)
		if due != nil {
			key += "|" + due.String()
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, proposal)
		if len(result) >= 100 {
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result
}
