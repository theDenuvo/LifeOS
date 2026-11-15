package httptransport

import (
	"net/http"
)

// reminderBody is what the browser receives, both from the live event and from the
// catch-up endpoint, so one client handler can show either.
type reminderBody map[string]any

// getDueReminders handles GET /api/v1/reminders.
//
// This is the durable side of delivery: it answers with every reminder that is due
// and was not acknowledged yet, so a client that was closed when the moment passed
// still receives it. Delivery is not recorded here; the client confirms with an
// acknowledgement once the reminder is on screen.
func (h *Handler) getDueReminders(w http.ResponseWriter, r *http.Request) {
	if h.services.Planning == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "planning is not configured")
		return
	}
	now := h.now()
	due, err := h.services.Planning.ActionableReminders(r.Context(), now)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	data := make([]reminderBody, 0, len(due))
	for _, item := range due {
		if item.Task == nil {
			continue
		}
		value := item.Task
		body := reminderBody{
			"reminder_id":  item.Reminder.ID,
			"task_id":      value.ID,
			"title":        value.Title,
			"kind":         string(item.Reminder.Kind),
			"priority":     string(value.Priority),
			"fire_at":      formatTime(item.Reminder.FireAt),
			"delivered":    false,
			"overdue":      value.IsOverdue(now),
			"notified":     item.Reminder.NotifiedAt != nil,
			"available_at": formatTime(item.Reminder.FireAt),
		}
		if value.Note != nil {
			body["note"] = *value.Note
		}
		if value.EstimateMinutes != nil {
			body["estimate_minutes"] = *value.EstimateMinutes
		}
		if value.DueAt != nil {
			body["due_at"] = formatTime(*value.DueAt)
		}
		if value.DueDate != nil && !value.DueDate.IsZero() {
			body["due_date"] = value.DueDate.String()
		}
		if value.Repeat != nil {
			body["repeat"] = value.Repeat.Describe()
		}
		data = append(data, body)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "server_time": formatTime(now)})
}

// ackReminder handles POST /api/v1/reminders/{id}/ack.
//
// Acknowledging is what turns "we announced it" into "the user saw it". It is
// idempotent: acknowledging twice keeps the first moment, so a retried request from
// a reconnecting client cannot mark the reminder as delivered again later.
func (h *Handler) ackReminder(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if h.services.Planning == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "planning is not configured")
		return
	}
	if err := h.services.Planning.MarkReminderDelivered(r.Context(), id, h.now()); err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resyncReminders handles POST /api/v1/reminders/resync.
//
// It rebuilds the reminder schedule of open tasks that lost theirs, which is the
// recovery path for a reminder regeneration that failed after a task change.
func (h *Handler) resyncReminders(w http.ResponseWriter, r *http.Request) {
	if h.services.Planning == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "planning is not configured")
		return
	}
	repaired, err := h.services.Planning.ReconcileReminders(r.Context())
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"repaired": repaired, "checked_at": formatTime(h.now())})
}
