package httptransport

import (
	"net/http"
	"strconv"
	"strings"
)

// search handles GET /api/v1/search.
func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "q is required")
		return
	}

	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid limit")
			return
		}
		limit = value
	}

	result, err := h.services.Search.Query(r.Context(), query, limit)
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}

	taskBodies := make([]taskBody, 0, len(result.Tasks))
	for _, item := range result.Tasks {
		taskBodies = append(taskBodies, newTaskBody(item, h.now()))
	}
	inboxBodies := make([]inboxBody, 0, len(result.Inbox))
	for _, item := range result.Inbox {
		inboxBodies = append(inboxBodies, newInboxBody(item))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"tasks": taskBodies,
		"inbox": inboxBodies,
		"meta":  map[string]any{"total": result.Total, "limit": limit},
	})
}
