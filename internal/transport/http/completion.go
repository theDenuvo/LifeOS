package httptransport

import (
	"errors"
	"net/http"

	"github.com/theDenuvo/LifeOS/internal/application/completion"
)

// completionReportBody is the API shape of a completion check.
type completionReportBody struct {
	ID         int64                     `json:"id,omitempty"`
	TaskID     int64                     `json:"task_id"`
	Title      string                    `json:"title"`
	Expected   string                    `json:"expected_result,omitempty"`
	Verdict    string                    `json:"verdict"`
	Confidence float64                   `json:"confidence"`
	Reason     string                    `json:"reason"`
	Evidence   []completion.Evidence     `json:"evidence"`
	Sources    []completion.SourceStatus `json:"sources"`
	Decision   string                    `json:"decision"`
	CheckedAt  string                    `json:"checked_at"`
	Completed  bool                      `json:"completed,omitempty"`
}

func newCompletionReportBody(report completion.Report, completed bool) completionReportBody {
	evidence := report.Evidence
	if evidence == nil {
		evidence = []completion.Evidence{}
	}
	sources := report.Sources
	if sources == nil {
		sources = []completion.SourceStatus{}
	}
	body := completionReportBody{
		ID:         report.ID,
		TaskID:     report.TaskID,
		Title:      report.Title,
		Expected:   report.Expected,
		Verdict:    string(report.Verdict),
		Confidence: report.Confidence,
		Reason:     report.Reason,
		Evidence:   evidence,
		Sources:    sources,
		Decision:   report.Decision,
		CheckedAt:  formatTime(report.CheckedAt),
		Completed:  completed,
	}
	return body
}

// verifyTaskCompletion handles POST /api/v1/tasks/{id}/verify.
//
// The check reports what the available sources say and keeps the grounds for the
// conclusion. It does not change the task: confirming is the user's decision, and
// automatic confirmation requires the explicit setting.
func (h *Handler) verifyTaskCompletion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if h.services.Completion == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "completion checks are not configured")
		return
	}
	report, completed, err := h.services.Completion.CompleteIfVerified(r.Context(), id)
	if err != nil {
		h.writeCompletionError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newCompletionReportBody(report, completed))
}

// decideTaskCompletion handles POST /api/v1/tasks/{id}/verify/decision.
//
// Accepting closes the task, declining keeps it open and is remembered so the next
// check does not insist.
func (h *Handler) decideTaskCompletion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if h.services.Completion == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "completion checks are not configured")
		return
	}
	var body struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	report, err := h.services.Completion.Decide(r.Context(), id, body.Decision)
	if err != nil {
		h.writeCompletionError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newCompletionReportBody(report, report.Decision == completion.DecisionAccepted && report.Verdict != completion.VerdictAlreadyCompleted))
}

// getTaskVerification handles GET /api/v1/tasks/{id}/verification.
func (h *Handler) getTaskVerification(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if h.services.Completion == nil {
		h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "completion checks are not configured")
		return
	}
	report, found, err := h.services.Completion.Latest(r.Context(), id)
	if err != nil {
		h.writeCompletionError(w, r, err)
		return
	}
	if !found {
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "no completion check for this task yet")
		return
	}
	h.writeJSON(w, http.StatusOK, newCompletionReportBody(report, false))
}

func (h *Handler) writeCompletionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, completion.ErrInvalidDecision):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeTaskError(w, r, err)
	}
}
