package httptransport

import (
	"net/http"
	"strings"
	"time"
)

// listInsights handles GET /api/v1/insights.
func (h *Handler) listInsights(w http.ResponseWriter, r *http.Request) {
	insights, err := h.services.Proactive.Compute(r.Context())
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": insights})
}

// dismissInsight handles POST /api/v1/insights/{key}/dismiss.
func (h *Handler) dismissInsight(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "insight key is required")
		return
	}
	if err := h.services.Proactive.Dismiss(r.Context(), key); err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// snoozeInsight handles POST /api/v1/insights/{key}/snooze.
func (h *Handler) snoozeInsight(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "insight key is required")
		return
	}

	var body struct {
		Until *string `json:"until"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil && err != errEmptyBody {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	var until time.Time
	if body.Until != nil && *body.Until != "" {
		parsed, err := time.Parse(time.RFC3339, *body.Until)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "until must be RFC3339")
			return
		}
		until = parsed.UTC()
	}

	if err := h.services.Proactive.Snooze(r.Context(), key, until); err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
