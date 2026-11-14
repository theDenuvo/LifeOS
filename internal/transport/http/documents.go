package httptransport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	"github.com/theDenuvo/LifeOS/internal/domain/document"
)

type documentBody struct {
	ID        int64          `json:"id"`
	Filename  string         `json:"filename"`
	MIME      string         `json:"mime"`
	Size      int64          `json:"size"`
	Status    string         `json:"status"`
	Error     *string        `json:"error"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	Tasks     []proposalBody `json:"tasks,omitempty"`
}

type proposalBody struct {
	ID         int64   `json:"id"`
	DocumentID int64   `json:"document_id"`
	Title      string  `json:"title"`
	Note       *string `json:"note"`
	DueDate    *string `json:"due_date"`
	Priority   string  `json:"priority"`
	Confidence float64 `json:"confidence"`
	Source     *string `json:"source"`
	Status     string  `json:"status"`
	TaskID     *int64  `json:"task_id"`
}

func newDocumentBody(value *document.Document) documentBody {
	return documentBody{
		ID:        value.ID,
		Filename:  value.Filename,
		MIME:      value.MIME,
		Size:      value.Size,
		Status:    string(value.Status),
		Error:     value.Error,
		CreatedAt: formatTime(value.CreatedAt),
		UpdatedAt: formatTime(value.UpdatedAt),
	}
}

func newProposalBody(value *document.ProposedTask) proposalBody {
	body := proposalBody{
		ID:         value.ID,
		DocumentID: value.DocumentID,
		Title:      value.Title,
		Note:       value.Note,
		Priority:   string(value.Priority),
		Confidence: value.Confidence,
		Source:     value.Source,
		Status:     string(value.Status),
		TaskID:     value.TaskID,
	}
	if value.DueDate != nil && !value.DueDate.IsZero() {
		date := value.DueDate.String()
		body.DueDate = &date
	}
	return body
}

// uploadDocument handles POST /api/v1/documents (multipart/form-data).
func (h *Handler) uploadDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, appdocuments.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid multipart upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "file field is required")
		return
	}
	defer file.Close()

	created, err := h.services.Documents.Upload(r.Context(), header.Filename, header.Header.Get("Content-Type"), file, header.Size)
	if err != nil {
		h.writeDocumentError(w, r, err)
		return
	}

	go h.processDocument(created.ID)
	h.writeJSON(w, http.StatusAccepted, newDocumentBody(created))
}

func (h *Handler) processDocument(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := h.services.Documents.Process(ctx, id); err != nil {
		slog.Error("document processing failed", "id", id, "error", err)
	}
}

// listDocuments handles GET /api/v1/documents.
func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			limit = value
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			offset = value
		}
	}
	values, total, err := h.services.Documents.List(r.Context(), limit, offset)
	if err != nil {
		h.writeDocumentError(w, r, err)
		return
	}
	data := make([]documentBody, 0, len(values))
	for _, value := range values {
		data = append(data, newDocumentBody(value))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": limit, "offset": offset, "total": total},
	})
}

// getDocument handles GET /api/v1/documents/{id}.
func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Documents.Get(r.Context(), id)
	if err != nil {
		h.writeDocumentError(w, r, err)
		return
	}
	body := newDocumentBody(view.Document)
	body.Tasks = make([]proposalBody, 0, len(view.Tasks))
	for _, proposal := range view.Tasks {
		body.Tasks = append(body.Tasks, newProposalBody(proposal))
	}
	h.writeJSON(w, http.StatusOK, body)
}

// acceptDocumentTasks handles POST /api/v1/documents/{id}/tasks.
func (h *Handler) acceptDocumentTasks(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		TaskIDs []int64 `json:"task_ids"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	result, err := h.services.Documents.Accept(r.Context(), id, body.TaskIDs)
	if err != nil {
		h.writeDocumentError(w, r, err)
		return
	}
	now := h.now()
	tasks := make([]taskBody, 0, len(result.Tasks))
	for _, item := range result.Tasks {
		tasks = append(tasks, newTaskBody(item, now))
	}
	proposals := make([]proposalBody, 0, len(result.Proposals))
	for _, proposal := range result.Proposals {
		proposals = append(proposals, newProposalBody(proposal))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "proposals": proposals})
}

// reprocessDocument handles POST /api/v1/documents/{id}/reprocess.
func (h *Handler) reprocessDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.services.Documents.Retry(r.Context(), id); err != nil {
		h.writeDocumentError(w, r, err)
		return
	}
	go h.processDocument(id)
	h.writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "pending"})
}

// deleteDocument handles DELETE /api/v1/documents/{id}.
func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.services.Documents.Delete(r.Context(), id); err != nil {
		h.writeDocumentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeDocumentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appdocuments.ErrDocumentNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Document not found")
	case errors.Is(err, appdocuments.ErrFileTooLarge):
		h.writeProblem(w, r, http.StatusRequestEntityTooLarge, "file-too-large", "File Too Large", err.Error())
	case errors.Is(err, appdocuments.ErrUnsupportedFile):
		h.writeProblem(w, r, http.StatusUnprocessableEntity, "unsupported-file", "Unsupported File", err.Error())
	case errors.Is(err, appdocuments.ErrLLMNotConfigured):
		h.writeProblem(w, r, http.StatusServiceUnavailable, "llm-not-configured", "LLM Not Configured", err.Error())
	case errors.Is(err, appdocuments.ErrNoTasks):
		h.writeProblem(w, r, http.StatusBadRequest, "no-tasks", "No Tasks", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}
