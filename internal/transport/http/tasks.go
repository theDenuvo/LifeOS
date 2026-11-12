package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

const maxRequestBodyBytes = 1 << 20

// taskBody is the shared wire shape for a task.
//
// Version and ETag are extensions to the documented Task schema: they expose the
// optimistic-locking token the client must send back in If-Match. Adding fields
// is backward compatible; the contract documents them.
type taskBody struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Note        *string  `json:"note"`
	Priority    string   `json:"priority"`
	Status      string   `json:"status"`
	PlannedDate *string  `json:"planned_date"`
	DueDate     *string  `json:"due_date"`
	DueAt       *string  `json:"due_at"`
	Timezone    string   `json:"timezone"`
	Tags        []string `json:"tags"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	CompletedAt *string  `json:"completed_at"`
	CancelledAt *string  `json:"cancelled_at"`
	Overdue     bool     `json:"overdue"`
	Version     int      `json:"version"`
}

func newTaskBody(item tasks.TaskWithVersion, now time.Time) taskBody {
	t := item.Task
	body := taskBody{
		ID:        t.ID,
		Title:     t.Title,
		Note:      t.Note,
		Priority:  string(t.Priority),
		Status:    string(t.Status),
		Timezone:  t.Timezone,
		Tags:      t.Tags,
		CreatedAt: formatTime(t.CreatedAt),
		UpdatedAt: formatTime(t.UpdatedAt),
		Overdue:   t.IsOverdue(now),
		Version:   item.Version,
	}
	if body.Tags == nil {
		body.Tags = []string{}
	}
	if t.PlannedDate != nil && !t.PlannedDate.IsZero() {
		value := t.PlannedDate.String()
		body.PlannedDate = &value
	}
	if t.DueDate != nil && !t.DueDate.IsZero() {
		value := t.DueDate.String()
		body.DueDate = &value
	}
	if t.DueAt != nil {
		value := formatTime(*t.DueAt)
		body.DueAt = &value
	}
	if t.CompletedAt != nil {
		value := formatTime(*t.CompletedAt)
		body.CompletedAt = &value
	}
	if t.CancelledAt != nil {
		value := formatTime(*t.CancelledAt)
		body.CancelledAt = &value
	}
	return body
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

// listTasks handles GET /api/v1/tasks.
func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	filter, err := parseTaskFilter(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	result, err := h.tasks.ListTasks(r.Context(), filter)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	now := h.now()
	data := make([]taskBody, 0, len(result.Tasks))
	for _, item := range result.Tasks {
		data = append(data, newTaskBody(item, now))
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

// createTask handles POST /api/v1/tasks.
func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string   `json:"title"`
		Note        *string  `json:"note"`
		Priority    *string  `json:"priority"`
		PlannedDate *string  `json:"planned_date"`
		DueDate     *string  `json:"due_date"`
		DueAt       *string  `json:"due_at"`
		Timezone    *string  `json:"timezone"`
		Tags        []string `json:"tags"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	in := tasks.CreateTaskInput{
		Title:    body.Title,
		Note:     optionalString(body.Note),
		Timezone: optionalString(body.Timezone),
		Tags:     body.Tags,
	}
	if body.Priority != nil {
		in.Priority = tasks.Some(task.Priority(*body.Priority))
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

	created, err := h.tasks.CreateTask(r.Context(), in, task.ActorUser)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	writeETag(w, created.Version)
	h.writeJSON(w, http.StatusCreated, newTaskBody(created, h.now()))
}

// getTask handles GET /api/v1/tasks/{id}.
func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	found, err := h.tasks.GetTask(r.Context(), id)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	writeETag(w, found.Version)
	h.writeJSON(w, http.StatusOK, newTaskBody(found, h.now()))
}

// patchTask handles PATCH /api/v1/tasks/{id}.
func (h *Handler) patchTask(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	expected, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}

	in, err := parseUpdateTaskInput(w, r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	updated, err := h.tasks.UpdateTask(r.Context(), id, expected, in, task.ActorUser)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	writeETag(w, updated.Version)
	h.writeJSON(w, http.StatusOK, newTaskBody(updated, h.now()))
}

// completeTask handles POST /api/v1/tasks/{id}/complete.
func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) {
	h.applyTransition(w, r, h.tasks.CompleteTask)
}

// cancelTask handles POST /api/v1/tasks/{id}/cancel.
func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request) {
	h.applyTransition(w, r, h.tasks.CancelTask)
}

// restoreTask handles POST /api/v1/tasks/{id}/restore.
func (h *Handler) restoreTask(w http.ResponseWriter, r *http.Request) {
	h.applyTransition(w, r, h.tasks.RestoreTask)
}

type transitionFunc func(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (tasks.TaskWithVersion, error)

func (h *Handler) applyTransition(w http.ResponseWriter, r *http.Request, apply transitionFunc) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	expected, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}

	updated, err := apply(r.Context(), id, expected, task.ActorUser)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	writeETag(w, updated.Version)
	h.writeJSON(w, http.StatusOK, newTaskBody(updated, h.now()))
}

// rescheduleTask handles POST /api/v1/tasks/{id}/reschedule.
func (h *Handler) rescheduleTask(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	expected, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}

	var body map[string]json.RawMessage
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	var in tasks.RescheduleTaskInput
	for field, raw := range body {
		switch field {
		case "planned_date":
			value, err := decodeOptionalCalendarDate(raw)
			if err != nil {
				h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "planned_date: "+err.Error())
				return
			}
			in.PlannedDate = value
		case "due_date":
			value, err := decodeOptionalCalendarDate(raw)
			if err != nil {
				h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "due_date: "+err.Error())
				return
			}
			in.DueDate = value
		case "due_at":
			value, err := decodeOptionalTime(raw)
			if err != nil {
				h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "due_at: "+err.Error())
				return
			}
			in.DueAt = value
		case "timezone":
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed",
				"timezone is not accepted by reschedule; use PATCH to change the task timezone")
			return
		default:
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed",
				fmt.Sprintf("unknown field %q", field))
			return
		}
	}

	updated, err := h.tasks.RescheduleTask(r.Context(), id, expected, in, task.ActorUser)
	if err != nil {
		h.writeTaskError(w, r, err)
		return
	}

	writeETag(w, updated.Version)
	h.writeJSON(w, http.StatusOK, newTaskBody(updated, h.now()))
}

// parseTaskFilter converts query parameters into a typed filter.
func parseTaskFilter(r *http.Request) (tasks.TaskFilter, error) {
	query := r.URL.Query()
	var filter tasks.TaskFilter

	for _, raw := range query["status"] {
		for _, value := range splitCSV(raw) {
			status := task.Status(value)
			if !status.IsValid() {
				return tasks.TaskFilter{}, fmt.Errorf("invalid status %q", value)
			}
			filter.Statuses = append(filter.Statuses, status)
		}
	}

	for _, raw := range query["priority"] {
		for _, value := range splitCSV(raw) {
			priority := task.Priority(value)
			if !priority.IsValid() {
				return tasks.TaskFilter{}, fmt.Errorf("invalid priority %q", value)
			}
			filter.Priorities = append(filter.Priorities, priority)
		}
	}

	if raw := strings.TrimSpace(query.Get("planned_date")); raw != "" {
		date, err := task.ParseCalendarDate(raw)
		if err != nil {
			return tasks.TaskFilter{}, fmt.Errorf("invalid planned_date %q", raw)
		}
		filter.PlannedDate = tasks.Some(date)
	}

	if raw := strings.TrimSpace(query.Get("due_date")); raw != "" {
		date, err := task.ParseCalendarDate(raw)
		if err != nil {
			return tasks.TaskFilter{}, fmt.Errorf("invalid due_date %q", raw)
		}
		filter.DueDate = tasks.Some(date)
	}

	if raw := strings.TrimSpace(query.Get("overdue")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return tasks.TaskFilter{}, fmt.Errorf("invalid overdue %q", raw)
		}
		filter.Overdue = tasks.Some(value)
	}

	if raw := strings.TrimSpace(query.Get("search")); raw != "" {
		filter.Search = tasks.Some(raw)
	}

	for _, raw := range query["tags"] {
		filter.Tags = append(filter.Tags, splitCSV(raw)...)
	}

	for _, raw := range query["sort"] {
		for _, value := range splitCSV(raw) {
			parsed, err := tasks.ParseSort(value)
			if err != nil {
				return tasks.TaskFilter{}, fmt.Errorf("invalid sort %q", value)
			}
			filter.Sorts = append(filter.Sorts, parsed)
		}
	}

	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return tasks.TaskFilter{}, fmt.Errorf("invalid limit %q", raw)
		}
		filter.Limit = tasks.Some(value)
	}

	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return tasks.TaskFilter{}, fmt.Errorf("invalid offset %q", raw)
		}
		filter.Offset = tasks.Some(value)
	}

	return filter, nil
}

// parseUpdateTaskInput reads a partial update, distinguishing absent fields
// (leave unchanged) from explicit nulls (clear the field).
func parseUpdateTaskInput(w http.ResponseWriter, r *http.Request) (tasks.UpdateTaskInput, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSONBody(w, r, &raw); err != nil {
		return tasks.UpdateTaskInput{}, err
	}

	var in tasks.UpdateTaskInput
	for field, value := range raw {
		switch field {
		case "title":
			var title string
			if err := json.Unmarshal(value, &title); err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("title must be a string")
			}
			in.Title = tasks.Some(title)
		case "note":
			note, err := decodeOptionalString(value)
			if err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("note: %w", err)
			}
			in.Note = note
		case "priority":
			var priority string
			if err := json.Unmarshal(value, &priority); err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("priority must be a string")
			}
			in.Priority = tasks.Some(task.Priority(priority))
		case "planned_date":
			date, err := decodeOptionalCalendarDate(value)
			if err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("planned_date: %w", err)
			}
			in.PlannedDate = date
		case "due_date":
			date, err := decodeOptionalCalendarDate(value)
			if err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("due_date: %w", err)
			}
			in.DueDate = date
		case "due_at":
			dueAt, err := decodeOptionalTime(value)
			if err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("due_at: %w", err)
			}
			in.DueAt = dueAt
		case "timezone":
			var timezone string
			if err := json.Unmarshal(value, &timezone); err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("timezone must be a string")
			}
			in.Timezone = tasks.Some(timezone)
		case "tags":
			var tags []string
			if err := json.Unmarshal(value, &tags); err != nil {
				return tasks.UpdateTaskInput{}, fmt.Errorf("tags must be an array of strings")
			}
			in.Tags = tasks.Some(tags)
		case "status":
			return tasks.UpdateTaskInput{}, errors.New("status is not accepted by PATCH; use /complete, /cancel or /restore")
		default:
			return tasks.UpdateTaskInput{}, fmt.Errorf("unknown field %q", field)
		}
	}
	return in, nil
}

// pathID parses the {id} path segment.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid task id")
		return 0, false
	}
	return id, true
}

// expectedVersion reads the optimistic-locking token from If-Match.
//
// A missing header is 428 Precondition Required rather than a silent
// last-write-wins update: the contract promises optimistic locking, so an
// unconditional mutation must be rejected.
func (h *Handler) expectedVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		h.writeProblem(w, r, http.StatusPreconditionRequired, "precondition-required", "Precondition Required",
			"If-Match with the task version is required for mutations")
		return 0, false
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed",
			"If-Match must contain a positive task version")
		return 0, false
	}
	return version, true
}

// writeTaskError maps application and domain errors onto problem responses.
func (h *Handler) writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tasks.ErrTaskNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Task not found")
	case errors.Is(err, tasks.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusConflict, "conflict", "Conflict",
			"The task was modified; re-read it and retry with the current version")
	case errors.Is(err, tasks.ErrInvalidLimit), errors.Is(err, tasks.ErrInvalidOffset), errors.Is(err, tasks.ErrInvalidSort):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case isInvalidTransition(err):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "precondition-failed", "Precondition Failed", err.Error())
	case isDomainValidationError(err):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}

func isInvalidTransition(err error) bool {
	var transition *task.InvalidTransitionError
	return errors.As(err, &transition)
}

func isDomainValidationError(err error) bool {
	return errors.Is(err, task.ErrInvalidTitle) ||
		errors.Is(err, task.ErrInvalidNote) ||
		errors.Is(err, task.ErrInvalidPriority) ||
		errors.Is(err, task.ErrInvalidStatus) ||
		errors.Is(err, task.ErrInvalidTimezone) ||
		errors.Is(err, task.ErrInvalidDate) ||
		errors.Is(err, task.ErrInvalidPlannedDate) ||
		errors.Is(err, task.ErrInvalidDueDate) ||
		errors.Is(err, task.ErrInvalidDueAtFormat) ||
		errors.Is(err, task.ErrInvalidTag) ||
		errors.Is(err, task.ErrInvalidEventType) ||
		errors.Is(err, task.ErrInvalidActor)
}

func writeETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, version))
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if decoder.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func optionalString(value *string) tasks.Optional[string] {
	if value == nil {
		return tasks.None[string]()
	}
	return tasks.Some(*value)
}

func optionalCalendarDate(value *string) (tasks.Optional[task.CalendarDate], error) {
	if value == nil {
		return tasks.None[task.CalendarDate](), nil
	}
	date, err := task.ParseCalendarDate(*value)
	if err != nil {
		return tasks.None[task.CalendarDate](), err
	}
	return tasks.Some(date), nil
}

func optionalTime(value *string) (tasks.Optional[time.Time], error) {
	if value == nil {
		return tasks.None[time.Time](), nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return tasks.None[time.Time](), errors.New("must be an RFC3339 timestamp")
	}
	return tasks.Some(parsed.UTC()), nil
}

func decodeOptionalString(raw json.RawMessage) (tasks.Optional[string], error) {
	if isJSONNull(raw) {
		return tasks.Some(""), nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return tasks.None[string](), errors.New("must be a string or null")
	}
	return tasks.Some(value), nil
}

func decodeOptionalCalendarDate(raw json.RawMessage) (tasks.Optional[task.CalendarDate], error) {
	if isJSONNull(raw) {
		return tasks.Some(task.CalendarDate{}), nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return tasks.None[task.CalendarDate](), errors.New("must be a YYYY-MM-DD string or null")
	}
	date, err := task.ParseCalendarDate(value)
	if err != nil {
		return tasks.None[task.CalendarDate](), errors.New("must be a valid YYYY-MM-DD date")
	}
	return tasks.Some(date), nil
}

func decodeOptionalTime(raw json.RawMessage) (tasks.Optional[time.Time], error) {
	if isJSONNull(raw) {
		return tasks.Some(time.Time{}), nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return tasks.None[time.Time](), errors.New("must be an RFC3339 timestamp or null")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return tasks.None[time.Time](), errors.New("must be a valid RFC3339 timestamp")
	}
	return tasks.Some(parsed.UTC()), nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
