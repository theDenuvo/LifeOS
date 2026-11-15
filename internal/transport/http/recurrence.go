package httptransport

import (
	"net/http"
	"strconv"
	"strings"
)

// listPendingRecurrences handles GET /api/v1/recurrence/pending.
//
// A recurring task whose next occurrence is missing is a state the user can see
// and act on, instead of the continuation quietly disappearing after a failure.
func (h *Handler) listPendingRecurrences(w http.ResponseWriter, r *http.Request) {
	if h.services.Recurrence == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "recurrence is not configured")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "limit must be 1-100")
			return
		}
		limit = parsed
	}
	pending, err := h.services.Recurrence.Pending(r.Context(), limit)
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal-error", "Internal Error", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(pending))
	for _, item := range pending {
		data = append(data, map[string]any{
			"task_id": item.TaskID,
			"title":   item.Title,
			"reason":  item.Reason,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// reconcileRecurrences handles POST /api/v1/recurrence/reconcile.
//
// It retries the missing next occurrences now instead of waiting for the next
// background pass, and reports what could still not be created.
func (h *Handler) reconcileRecurrences(w http.ResponseWriter, r *http.Request) {
	if h.services.Recurrence == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "recurrence is not configured")
		return
	}
	result, err := h.services.Recurrence.Reconcile(r.Context(), 50)
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal-error", "Internal Error", err.Error())
		return
	}
	pending := make([]map[string]any, 0, len(result.Pending))
	for _, item := range result.Pending {
		pending = append(pending, map[string]any{
			"task_id": item.TaskID,
			"title":   item.Title,
			"reason":  item.Reason,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"checked": result.Checked,
		"created": result.Created,
		"pending": pending,
	})
}
