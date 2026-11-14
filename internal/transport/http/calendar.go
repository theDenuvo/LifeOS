package httptransport

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

type eventBody struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	Calendar    string  `json:"calendar"`
	StartsAt    string  `json:"starts_at"`
	EndsAt      string  `json:"ends_at"`
	AllDay      bool    `json:"all_day"`
	Timezone    string  `json:"timezone"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Version     int     `json:"version"`
}

func newEventBody(item appevents.EventWithVersion) eventBody {
	value := item.Event
	return eventBody{
		ID:          value.ID,
		Title:       value.Title,
		Description: value.Description,
		Location:    value.Location,
		Calendar:    string(value.Calendar),
		StartsAt:    formatTime(value.StartsAt),
		EndsAt:      formatTime(value.EndsAt),
		AllDay:      value.AllDay,
		Timezone:    value.Timezone,
		CreatedAt:   formatTime(value.CreatedAt),
		UpdatedAt:   formatTime(value.UpdatedAt),
		Version:     item.Version,
	}
}

type conflictBody struct {
	Kind     string  `json:"kind"`
	EventIDs []int64 `json:"event_ids"`
	TaskIDs  []int64 `json:"task_ids,omitempty"`
	StartsAt string  `json:"starts_at"`
	EndsAt   string  `json:"ends_at"`
}

// getCalendar handles GET /api/v1/calendar.
func (h *Handler) getCalendar(w http.ResponseWriter, r *http.Request) {
	from, to, err := h.parseDateRange(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	calendars, err := parseCalendars(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	result, err := h.services.Calendar.Range(r.Context(), from, to, calendars)
	if err != nil {
		h.writeEventError(w, r, err)
		return
	}

	events := make([]eventBody, 0, len(result.Events))
	for _, item := range result.Events {
		events = append(events, newEventBody(item))
	}
	now := h.now()
	tasks := make([]taskBody, 0, len(result.Tasks))
	for _, item := range result.Tasks {
		tasks = append(tasks, newTaskBody(item, now))
	}
	conflicts := make([]conflictBody, 0, len(result.Conflicts))
	for _, item := range result.Conflicts {
		conflicts = append(conflicts, conflictBody{
			Kind:     item.Kind,
			EventIDs: item.EventIDs,
			TaskIDs:  item.TaskIDs,
			StartsAt: formatTime(item.StartsAt),
			EndsAt:   formatTime(item.EndsAt),
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"from":      result.From.String(),
		"to":        result.To.String(),
		"events":    events,
		"tasks":     tasks,
		"conflicts": conflicts,
	})
}

// listEvents handles GET /api/v1/calendar/events.
func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	from, to, err := h.parseDateRange(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	calendars, err := parseCalendars(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	loc, err := time.LoadLocation(h.cfg.Timezone)
	if err != nil {
		loc = time.UTC
	}
	events, err := h.services.Events.ListRange(r.Context(), from.Time(loc), to.Time(loc).AddDate(0, 0, 1), calendars)
	if err != nil {
		h.writeEventError(w, r, err)
		return
	}

	data := make([]eventBody, 0, len(events))
	for _, item := range events {
		data = append(data, newEventBody(item))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{"total": len(data)}})
}

// createEvent handles POST /api/v1/calendar/events.
func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Location    *string `json:"location"`
		Calendar    string  `json:"calendar"`
		StartsAt    string  `json:"starts_at"`
		EndsAt      *string `json:"ends_at"`
		AllDay      bool    `json:"all_day"`
		Timezone    *string `json:"timezone"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	startsAt, err := time.Parse(time.RFC3339, body.StartsAt)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "starts_at must be an RFC3339 timestamp")
		return
	}
	var endsAt *time.Time
	if body.EndsAt != nil && strings.TrimSpace(*body.EndsAt) != "" {
		parsed, err := time.Parse(time.RFC3339, *body.EndsAt)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "ends_at must be an RFC3339 timestamp")
			return
		}
		endsAt = &parsed
	}

	created, err := h.services.Events.Create(r.Context(), appevents.CreateEventInput{
		Title:       body.Title,
		Description: body.Description,
		Location:    body.Location,
		Calendar:    body.Calendar,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		AllDay:      body.AllDay,
		Timezone:    body.Timezone,
	})
	if err != nil {
		h.writeEventError(w, r, err)
		return
	}

	writeETag(w, created.Version)
	h.writeJSON(w, http.StatusCreated, newEventBody(created))
}

// getEvent handles GET /api/v1/calendar/events/{id}.
func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	value, err := h.services.Events.Get(r.Context(), id)
	if err != nil {
		h.writeEventError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newEventBody(value))
}

// patchEvent handles PATCH /api/v1/calendar/events/{id}.
func (h *Handler) patchEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	expected, ok := h.expectedEventVersion(w, r)
	if !ok {
		return
	}

	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Location    *string `json:"location"`
		Calendar    *string `json:"calendar"`
		StartsAt    *string `json:"starts_at"`
		EndsAt      *string `json:"ends_at"`
		AllDay      *bool   `json:"all_day"`
		Timezone    *string `json:"timezone"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	in := appevents.UpdateEventInput{
		Title:       body.Title,
		Description: body.Description,
		Location:    body.Location,
		Calendar:    body.Calendar,
		AllDay:      body.AllDay,
		Timezone:    body.Timezone,
	}
	if body.StartsAt != nil {
		parsed, err := time.Parse(time.RFC3339, *body.StartsAt)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "starts_at must be an RFC3339 timestamp")
			return
		}
		in.StartsAt = &parsed
	}
	if body.EndsAt != nil {
		parsed, err := time.Parse(time.RFC3339, *body.EndsAt)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "ends_at must be an RFC3339 timestamp")
			return
		}
		in.EndsAt = &parsed
	}

	updated, err := h.services.Events.Update(r.Context(), id, expected, in)
	if err != nil {
		h.writeEventError(w, r, err)
		return
	}
	writeETag(w, updated.Version)
	h.writeJSON(w, http.StatusOK, newEventBody(updated))
}

// deleteEvent handles DELETE /api/v1/calendar/events/{id}.
func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	expected, ok := h.expectedEventVersion(w, r)
	if !ok {
		return
	}
	if err := h.services.Events.Delete(r.Context(), id, expected); err != nil {
		h.writeEventError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) expectedEventVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		h.writeProblem(w, r, http.StatusPreconditionRequired, "precondition-required", "Precondition Required",
			"If-Match with the event version is required for mutations")
		return 0, false
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed",
			"If-Match must contain a positive event version")
		return 0, false
	}
	return version, true
}

func (h *Handler) writeEventError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appevents.ErrEventNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Event not found")
	case errors.Is(err, appevents.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusConflict, "conflict", "Conflict",
			"The event was modified; re-read it and retry with the current version")
	case errors.Is(err, appevents.ErrInvalidDateRange), errors.Is(err, appevents.ErrInvalidCalendar):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case isEventValidationError(err):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}

func isEventValidationError(err error) bool {
	return errors.Is(err, event.ErrInvalidTitle) ||
		errors.Is(err, event.ErrInvalidDescription) ||
		errors.Is(err, event.ErrInvalidLocation) ||
		errors.Is(err, event.ErrInvalidCalendar) ||
		errors.Is(err, event.ErrInvalidTimezone) ||
		errors.Is(err, event.ErrInvalidRange) ||
		errors.Is(err, event.ErrRangeTooLong)
}

func parseCalendars(r *http.Request) ([]event.Calendar, error) {
	raw := r.URL.Query()["calendar"]
	if len(raw) == 0 {
		raw = r.URL.Query()["calendars"]
	}
	result := make([]event.Calendar, 0, 3)
	for _, item := range raw {
		for _, value := range splitCSV(item) {
			calendar := event.Calendar(value)
			if !calendar.IsValid() {
				return nil, fmt.Errorf("invalid calendar %q", value)
			}
			result = append(result, calendar)
		}
	}
	return result, nil
}

func (h *Handler) parseDateRange(r *http.Request) (task.CalendarDate, task.CalendarDate, error) {
	loc, err := time.LoadLocation(h.cfg.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	from := task.CalendarDate{}
	to := task.CalendarDate{}

	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, err := task.ParseCalendarDate(raw)
		if err != nil {
			return from, to, fmt.Errorf("invalid from %q", raw)
		}
		from = parsed
	} else {
		parsed, _ := task.ParseCalendarDate(now.Format("2006-01-02"))
		from = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, err := task.ParseCalendarDate(raw)
		if err != nil {
			return from, to, fmt.Errorf("invalid to %q", raw)
		}
		to = parsed
	} else {
		end := now.AddDate(0, 0, 6)
		parsed, _ := task.ParseCalendarDate(end.Format("2006-01-02"))
		to = parsed
	}
	if from.IsZero() || to.IsZero() || from.String() > to.String() {
		return from, to, errors.New("from must be on or before to")
	}
	return from, to, nil
}
