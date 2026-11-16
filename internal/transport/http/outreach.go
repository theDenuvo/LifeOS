package httptransport

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/proactive"
)

// Roadmap stage 5: proactive initiative. The outreach history and its
// reactions are visible to the user, which is what keeps the initiative
// reviewable instead of magical.

type outreachBody struct {
	ID             int64   `json:"id"`
	ConversationID int64   `json:"conversation_id"`
	MessageID      int64   `json:"message_id"`
	InsightKey     string  `json:"insight_key"`
	Kind           string  `json:"kind"`
	Severity       string  `json:"severity"`
	Title          string  `json:"title"`
	Reason         string  `json:"reason"`
	Proposal       string  `json:"proposal"`
	Outcome        string  `json:"outcome"`
	CreatedAt      string  `json:"created_at"`
	DecidedAt      *string `json:"decided_at,omitempty"`
	SnoozedUntil   *string `json:"snoozed_until,omitempty"`
}

func toOutreachBody(value *proactive.Outreach) outreachBody {
	body := outreachBody{
		ID:             value.ID,
		ConversationID: value.ConversationID,
		MessageID:      value.MessageID,
		InsightKey:     value.InsightKey,
		Kind:           value.Kind,
		Severity:       string(value.Severity),
		Title:          value.Title,
		Reason:         value.Reason,
		Proposal:       value.Proposal,
		Outcome:        string(value.Outcome),
		CreatedAt:      value.CreatedAt.Format(time.RFC3339),
	}
	if value.DecidedAt != nil {
		formatted := value.DecidedAt.Format(time.RFC3339)
		body.DecidedAt = &formatted
	}
	if value.SnoozedUntil != nil {
		formatted := value.SnoozedUntil.Format(time.RFC3339)
		body.SnoozedUntil = &formatted
	}
	return body
}

// listOutreach handles GET /outreach.
func (h *Handler) listOutreach(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 30)
	offset := queryInt(r, "offset", 0)
	items, total, err := h.services.Outreach.List(r.Context(), limit, offset)
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	data := make([]outreachBody, 0, len(items))
	for i := range items {
		data = append(data, toOutreachBody(&items[i]))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": total})
}

// getOutreach handles GET /outreach/{id}.
func (h *Handler) getOutreach(w http.ResponseWriter, r *http.Request) {
	id, ok := h.outreachID(w, r)
	if !ok {
		return
	}
	found, err := h.services.Outreach.Get(r.Context(), id)
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	if found == nil {
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "outreach not found")
		return
	}
	h.writeJSON(w, http.StatusOK, toOutreachBody(found))
}

// acknowledgeOutreach handles POST /outreach/{id}/ack.
func (h *Handler) acknowledgeOutreach(w http.ResponseWriter, r *http.Request) {
	id, ok := h.outreachID(w, r)
	if !ok {
		return
	}
	if err := h.services.Outreach.Acknowledge(r.Context(), id); err != nil {
		h.writeProblem(w, r, outreachErrorStatus(err), outreachErrorCode(err), "Outreach Error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dismissOutreach handles POST /outreach/{id}/dismiss.
func (h *Handler) dismissOutreach(w http.ResponseWriter, r *http.Request) {
	id, ok := h.outreachID(w, r)
	if !ok {
		return
	}
	if err := h.services.Outreach.Dismiss(r.Context(), id); err != nil {
		h.writeProblem(w, r, outreachErrorStatus(err), outreachErrorCode(err), "Outreach Error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// snoozeOutreach handles POST /outreach/{id}/snooze.
func (h *Handler) snoozeOutreach(w http.ResponseWriter, r *http.Request) {
	id, ok := h.outreachID(w, r)
	if !ok {
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
	if err := h.services.Outreach.Snooze(r.Context(), id, until); err != nil {
		h.writeProblem(w, r, outreachErrorStatus(err), outreachErrorCode(err), "Outreach Error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) outreachID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.PathValue("id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid outreach id")
		return 0, false
	}
	return id, true
}

// outreachErrorStatus maps application errors onto HTTP statuses.
func outreachErrorStatus(err error) int {
	if err == proactive.ErrOutreachNotFound {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func outreachErrorCode(err error) string {
	if err == proactive.ErrOutreachNotFound {
		return "not-found"
	}
	return "internal"
}
