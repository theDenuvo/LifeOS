package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domaininbox "github.com/theDenuvo/LifeOS/internal/domain/inbox"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// inboxBody is the wire shape of an inbox item.
type inboxBody struct {
	ID              int64           `json:"id"`
	Text            string          `json:"text"`
	Meta            json.RawMessage `json:"meta"`
	CreatedAt       string          `json:"created_at"`
	ProcessedAt     *string         `json:"processed_at"`
	ConvertedTaskID *int64          `json:"converted_task_id"`
}

func newInboxBody(item *domaininbox.Item) inboxBody {
	body := inboxBody{
		ID:              item.ID,
		Text:            item.Text,
		CreatedAt:       formatTime(item.CreatedAt),
		ConvertedTaskID: item.ConvertedTaskID,
	}
	if len(item.Meta) > 0 {
		body.Meta = item.Meta
	}
	if item.ProcessedAt != nil {
		value := formatTime(*item.ProcessedAt)
		body.ProcessedAt = &value
	}
	return body
}

// listInbox handles GET /api/v1/inbox.
func (h *Handler) listInbox(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := inbox.Filter{UnprocessedOnly: true}

	if raw := strings.TrimSpace(query.Get("unprocessed_only")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid unprocessed_only")
			return
		}
		filter.UnprocessedOnly = value
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid limit")
			return
		}
		filter.Limit = tasks.Some(value)
	}
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid offset")
			return
		}
		filter.Offset = tasks.Some(value)
	}

	result, err := h.services.Inbox.List(r.Context(), filter)
	if err != nil {
		h.writeInboxError(w, r, err)
		return
	}

	data := make([]inboxBody, 0, len(result.Items))
	for _, item := range result.Items {
		data = append(data, newInboxBody(item))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{
			"limit":  result.Limit,
			"offset": result.Offset,
			"total":  result.Total,
		},
	})
}

// captureInbox handles POST /api/v1/inbox.
func (h *Handler) captureInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string          `json:"text"`
		Meta json.RawMessage `json:"meta"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	item, err := h.services.Inbox.Capture(r.Context(), body.Text, body.Meta)
	if err != nil {
		h.writeInboxError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, newInboxBody(item))
}

// convertInbox handles POST /api/v1/inbox/{id}/convert.
func (h *Handler) convertInbox(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	var body struct {
		Priority    *string `json:"priority"`
		PlannedDate *string `json:"planned_date"`
		DueDate     *string `json:"due_date"`
		DueAt       *string `json:"due_at"`
		Timezone    *string `json:"timezone"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil && !errors.Is(err, errEmptyBody) {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	var in inbox.ConvertInput
	if body.Priority != nil {
		in.Priority = tasks.Some(task.Priority(*body.Priority))
	}
	if body.Timezone != nil {
		in.Timezone = tasks.Some(*body.Timezone)
	}
	planned, err := optionalCalendarDate(body.PlannedDate)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "planned_date: "+err.Error())
		return
	}
	in.PlannedDate = planned
	due, err := optionalCalendarDate(body.DueDate)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "due_date: "+err.Error())
		return
	}
	in.DueDate = due
	dueAt, err := optionalTime(body.DueAt)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "due_at: "+err.Error())
		return
	}
	in.DueAt = dueAt

	created, err := h.services.Inbox.ConvertToTask(r.Context(), id, in)
	if err != nil {
		h.writeInboxError(w, r, err)
		return
	}

	writeETag(w, created.Version)
	h.writeJSON(w, http.StatusCreated, newTaskBody(created, h.now()))
}

// dismissInbox handles DELETE /api/v1/inbox/{id}.
func (h *Handler) dismissInbox(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.services.Inbox.Dismiss(r.Context(), id); err != nil {
		h.writeInboxError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// todaySummary handles GET /api/v1/summary/today.
func (h *Handler) todaySummary(w http.ResponseWriter, r *http.Request) {
	result, err := h.services.Summary.Today(r.Context())
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

// writeInboxError maps inbox errors onto problem responses.
func (h *Handler) writeInboxError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, inbox.ErrItemNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Inbox item not found")
	case errors.Is(err, inbox.ErrAlreadyProcessed):
		h.writeProblem(w, r, http.StatusConflict, "conflict", "Conflict", "Inbox item was already processed")
	case errors.Is(err, inbox.ErrInvalidLimit), errors.Is(err, inbox.ErrInvalidOffset):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case errors.Is(err, domaininbox.ErrInvalidText), errors.Is(err, domaininbox.ErrInvalidMeta):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case isDomainValidationError(err):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}
