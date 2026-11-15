package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// Roadmap stage 4: long-term goals and initiative management.

type goalMilestoneBody struct {
	ID          int64   `json:"id"`
	Position    int     `json:"position"`
	Title       string  `json:"title"`
	Criteria    string  `json:"criteria"`
	Status      string  `json:"status"`
	DueDate     *string `json:"due_date"`
	CompletedAt *string `json:"completed_at"`
	Version     int     `json:"version"`
}

type goalLinkBody struct {
	ID         int64  `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id"`
	Relation   string `json:"relation"`
	CreatedAt  string `json:"created_at"`
}

type goalProgressBody struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Summary    string  `json:"summary"`
	SourceType *string `json:"source_type,omitempty"`
	SourceID   *int64  `json:"source_id,omitempty"`
	RecordedAt string  `json:"recorded_at"`
}

type goalProposalBody struct {
	ID        int64           `json:"id"`
	Summary   string          `json:"summary"`
	Status    string          `json:"status"`
	Changes   json.RawMessage `json:"changes,omitempty"`
	CreatedAt string          `json:"created_at"`
	DecidedAt *string         `json:"decided_at,omitempty"`
}

type goalBody struct {
	ID          int64                `json:"id"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Status      string               `json:"status"`
	TargetDate  *string              `json:"target_date"`
	ApprovedAt  *string              `json:"approved_at"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
	Version     int                  `json:"version"`
	Milestones  []goalMilestoneBody  `json:"milestones"`
	Links       []goalLinkBody       `json:"links"`
	Proposals   []goalProposalBody   `json:"proposals"`
	ProgressLog []goalProgressBody   `json:"progress_log"`
	Progress    *goalProgressSummary `json:"progress,omitempty"`
	Signals     *goalSignals         `json:"signals,omitempty"`
}

type goalProgressSummary struct {
	MilestonesTotal int `json:"milestones_total"`
	MilestonesDone  int `json:"milestones_done"`
	// MilestonesCompleted counts done and skipped steps: the plan is closed,
	// but only MilestonesDone feeds the percentage.
	MilestonesCompleted int     `json:"milestones_completed"`
	LinkedTasks         int     `json:"linked_tasks"`
	LinkedTasksDone     int     `json:"linked_tasks_done"`
	Percent             *int    `json:"percent"`
	LastActivityAt      *string `json:"last_activity_at"`
}

type goalSignals struct {
	Stalled         bool   `json:"stalled"`
	StallReason     string `json:"stall_reason,omitempty"`
	Overdue         bool   `json:"overdue"`
	NoOpenWork      bool   `json:"no_open_work"`
	PendingProposal bool   `json:"pending_proposal"`
}

// formatGoalDate renders an optional calendar date.
func formatGoalDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	value := t.Format("2006-01-02")
	return &value
}

// parseGoalDate reads an optional YYYY-MM-DD calendar date.
func parseGoalDate(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return nil, errors.New("date must be YYYY-MM-DD")
	}
	t := parsed.UTC()
	return &t, nil
}

func newGoalBody(view *appgoals.View) goalBody {
	g := view.Goal
	body := goalBody{
		ID:          g.ID,
		Title:       g.Title,
		Description: g.Description,
		Status:      string(g.Status),
		TargetDate:  formatGoalDate(g.TargetDate),
		ApprovedAt:  formatOptionalTime(g.ApprovedAt),
		CreatedAt:   formatTime(g.CreatedAt),
		UpdatedAt:   formatTime(g.UpdatedAt),
		Version:     g.Version,
		Milestones:  []goalMilestoneBody{},
		Links:       []goalLinkBody{},
		Proposals:   []goalProposalBody{},
		ProgressLog: []goalProgressBody{},
	}
	for _, m := range view.Milestones {
		body.Milestones = append(body.Milestones, goalMilestoneBody{
			ID: m.ID, Position: m.Position, Title: m.Title, Criteria: m.Criteria,
			Status: string(m.Status), DueDate: formatGoalDate(m.DueDate),
			CompletedAt: formatOptionalTime(m.CompletedAt), Version: m.Version,
		})
	}
	for _, link := range view.Links {
		body.Links = append(body.Links, goalLinkBody{
			ID: link.ID, TargetType: string(link.TargetType), TargetID: link.TargetID,
			Relation: link.Relation, CreatedAt: formatTime(link.CreatedAt),
		})
	}
	for _, proposal := range view.Proposals {
		item := goalProposalBody{
			ID: proposal.ID, Summary: proposal.Summary, Status: string(proposal.Status),
			CreatedAt: formatTime(proposal.CreatedAt), DecidedAt: formatOptionalTime(proposal.DecidedAt),
		}
		if payload, err := json.Marshal(proposal.Payload); err == nil && len(payload) > 2 {
			item.Changes = payload
		}
		body.Proposals = append(body.Proposals, item)
	}
	for _, event := range view.ProgressLog {
		item := goalProgressBody{
			ID: event.ID, Kind: string(event.Kind), Summary: event.Summary,
			RecordedAt: formatTime(event.RecordedAt),
		}
		if event.SourceType != nil {
			kind := string(*event.SourceType)
			item.SourceType = &kind
			item.SourceID = event.SourceID
		}
		body.ProgressLog = append(body.ProgressLog, item)
	}
	if view.Review != nil {
		progress := view.Review.Progress
		body.Progress = &goalProgressSummary{
			MilestonesTotal:     progress.MilestonesTotal,
			MilestonesDone:      progress.MilestonesDone,
			MilestonesCompleted: progress.MilestonesCompleted,
			LinkedTasks:         progress.LinkedTasks,
			LinkedTasksDone:     progress.LinkedTasksDone,
			Percent:             progress.Percent,
			LastActivityAt:      formatOptionalTime(progress.LastActivityAt),
		}
		signals := view.Review.Signals
		body.Signals = &goalSignals{
			Stalled: signals.Stalled, StallReason: signals.StallReason,
			Overdue: signals.Overdue, NoOpenWork: signals.NoOpenWork,
			PendingProposal: signals.PendingProposal,
		}
	}
	return body
}

func formatOptionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	value := formatTime(*t)
	return &value
}

// listGoals handles GET /api/v1/goals.
func (h *Handler) listGoals(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	filter := appgoals.Filter{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := domaingoal.Status(raw)
		if !status.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
			return
		}
		filter.Status = &status
	}
	filter.Limit = queryInt(r, "limit", 30)
	filter.Offset = queryInt(r, "offset", 0)

	views, total, err := h.services.Goals.List(r.Context(), filter)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	data := make([]goalBody, 0, len(views))
	for _, view := range views {
		data = append(data, newGoalBody(view))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": filter.Limit, "offset": filter.Offset, "total": total},
	})
}

// createGoal handles POST /api/v1/goals.
func (h *Handler) createGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		TargetDate  string `json:"target_date"`
		Milestones  []struct {
			Title    string `json:"title"`
			Criteria string `json:"criteria"`
			DueDate  string `json:"due_date"`
		} `json:"milestones"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	input := appgoals.CreateInput{Title: body.Title, Description: body.Description}
	if target, err := parseGoalDate(body.TargetDate); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	} else {
		input.TargetDate = target
	}
	for _, milestone := range body.Milestones {
		ms := domaingoal.MilestoneInput{Title: milestone.Title, Criteria: milestone.Criteria}
		if due, err := parseGoalDate(milestone.DueDate); err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		} else {
			ms.DueDate = due
		}
		input.Milestones = append(input.Milestones, ms)
	}
	view, err := h.services.Goals.Create(r.Context(), input)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newGoalBody(view))
}

// getGoal handles GET /api/v1/goals/{id}.
func (h *Handler) getGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	view, err := h.services.Goals.Get(r.Context(), id)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// patchGoal handles PATCH /api/v1/goals/{id}.
func (h *Handler) patchGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		Title           *string `json:"title"`
		Description     *string `json:"description"`
		TargetDate      *string `json:"target_date"`
		ClearTarget     bool    `json:"clear_target_date"`
		ExpectedVersion int     `json:"expected_version"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	input := appgoals.UpdateInput{Title: body.Title, Description: body.Description, ClearTarget: body.ClearTarget}
	if body.TargetDate != nil {
		target, err := parseGoalDate(*body.TargetDate)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		}
		input.TargetDate = target
	}
	view, err := h.services.Goals.Update(r.Context(), id, body.ExpectedVersion, input)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// deleteGoal handles DELETE /api/v1/goals/{id}.
func (h *Handler) deleteGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	if err := h.services.Goals.Delete(r.Context(), id); err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// approveGoal handles POST /api/v1/goals/{id}/approve.
func (h *Handler) approveGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	view, err := h.services.Goals.Approve(r.Context(), id)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// changeGoalStatus handles POST /api/v1/goals/{id}/status.
func (h *Handler) changeGoalStatus(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	status := domaingoal.Status(body.Status)
	if !status.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
		return
	}
	view, err := h.services.Goals.SetStatus(r.Context(), id, status)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// createGoalMilestone handles POST /api/v1/goals/{id}/milestones.
func (h *Handler) createGoalMilestone(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		Title    string `json:"title"`
		Criteria string `json:"criteria"`
		DueDate  string `json:"due_date"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	input := domaingoal.MilestoneInput{Title: body.Title, Criteria: body.Criteria}
	if due, err := parseGoalDate(body.DueDate); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	} else {
		input.DueDate = due
	}
	view, err := h.services.Goals.AddMilestone(r.Context(), id, input)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// patchGoalMilestone handles PATCH /api/v1/goals/{id}/milestones/{milestoneId}.
func (h *Handler) patchGoalMilestone(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	milestoneID, mErr := strconv.ParseInt(r.PathValue("milestoneId"), 10, 64)
	if err != nil || mErr != nil || id <= 0 || milestoneID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid ids")
		return
	}
	var body struct {
		Title           *string `json:"title"`
		Criteria        *string `json:"criteria"`
		Status          *string `json:"status"`
		DueDate         *string `json:"due_date"`
		ClearDue        bool    `json:"clear_due_date"`
		ExpectedVersion int     `json:"expected_version"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	input := appgoals.MilestoneUpdateInput{Title: body.Title, Criteria: body.Criteria, ClearDue: body.ClearDue}
	if body.Status != nil {
		status := domaingoal.MilestoneStatus(*body.Status)
		if !status.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid milestone status")
			return
		}
		input.Status = &status
	}
	if body.DueDate != nil {
		due, err := parseGoalDate(*body.DueDate)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		}
		input.DueDate = due
	}
	view, err := h.services.Goals.UpdateMilestone(r.Context(), id, milestoneID, body.ExpectedVersion, input)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// deleteGoalMilestone handles DELETE /api/v1/goals/{id}/milestones/{milestoneId}.
func (h *Handler) deleteGoalMilestone(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	milestoneID, mErr := strconv.ParseInt(r.PathValue("milestoneId"), 10, 64)
	if err != nil || mErr != nil || id <= 0 || milestoneID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid ids")
		return
	}
	view, err := h.services.Goals.RemoveMilestone(r.Context(), id, milestoneID)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// createGoalProposal handles POST /api/v1/goals/{id}/proposals.
func (h *Handler) createGoalProposal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		Summary     string  `json:"summary"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		TargetDate  string  `json:"target_date"`
		ClearTarget bool    `json:"clear_target_date"`
		Milestones  []struct {
			Title    string `json:"title"`
			Criteria string `json:"criteria"`
			DueDate  string `json:"due_date"`
		} `json:"add_milestones"`
		CompleteIDs []int64 `json:"complete_milestone_ids"`
		SkipIDs     []int64 `json:"skip_milestone_ids"`
		ReopenIDs   []int64 `json:"reopen_milestone_ids"`
		RemoveIDs   []int64 `json:"remove_milestone_ids"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	payload := domaingoal.ProposalPayload{
		Title: body.Title, Description: body.Description, ClearTargetDate: body.ClearTarget,
		CompleteMilestoneIDs: body.CompleteIDs, SkipMilestoneIDs: body.SkipIDs,
		ReopenMilestoneIDs: body.ReopenIDs, RemoveMilestoneIDs: body.RemoveIDs,
	}
	if target, err := parseGoalDate(body.TargetDate); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	} else {
		payload.TargetDate = target
	}
	for _, milestone := range body.Milestones {
		ms := domaingoal.MilestoneInput{Title: milestone.Title, Criteria: milestone.Criteria}
		if due, err := parseGoalDate(milestone.DueDate); err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		} else {
			ms.DueDate = due
		}
		payload.AddMilestones = append(payload.AddMilestones, ms)
	}
	proposal, err := h.services.Goals.ProposeChange(r.Context(), id, payload, body.Summary)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{"proposal": map[string]any{
		"id": proposal.ID, "status": string(proposal.Status), "summary": proposal.Summary,
	}})
}

// decideGoalProposal handles POST /api/v1/goals/{id}/proposals/{proposalId}/apply
// and .../reject.
func (h *Handler) decideGoalProposal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	proposalID, pErr := strconv.ParseInt(r.PathValue("proposalId"), 10, 64)
	if err != nil || pErr != nil || id <= 0 || proposalID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid ids")
		return
	}
	var view *appgoals.View
	decision := r.PathValue("decision")
	if decision == "" {
		// The mux routes apply and reject as distinct patterns, so the decision
		// is read from the tail of the request path.
		if strings.HasSuffix(r.URL.Path, "/reject") {
			decision = "reject"
		} else {
			decision = "apply"
		}
	}
	switch decision {
	case "apply":
		view, err = h.services.Goals.ApplyProposal(r.Context(), id, proposalID)
	case "reject":
		view, err = h.services.Goals.RejectProposal(r.Context(), id, proposalID)
	default:
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "unknown decision")
		return
	}
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// linkGoal handles POST /api/v1/goals/{id}/links.
func (h *Handler) linkGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		TargetType string `json:"target_type"`
		TargetID   int64  `json:"target_id"`
		Relation   string `json:"relation"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	targetType := domaingoal.TargetType(body.TargetType)
	if !targetType.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid target type")
		return
	}
	view, err := h.services.Goals.AddLink(r.Context(), id, appgoals.LinkInput{
		TargetType: targetType, TargetID: body.TargetID, Relation: body.Relation,
	})
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// unlinkGoal handles DELETE /api/v1/goals/{id}/links/{linkId}.
func (h *Handler) unlinkGoal(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	linkID, lErr := strconv.ParseInt(r.PathValue("linkId"), 10, 64)
	if err != nil || lErr != nil || id <= 0 || linkID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid ids")
		return
	}
	view, err := h.services.Goals.RemoveLink(r.Context(), id, linkID)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// recordGoalProgress handles POST /api/v1/goals/{id}/progress.
func (h *Handler) recordGoalProgress(w http.ResponseWriter, r *http.Request) {
	if h.services.Goals == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Goals are not configured")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid goal id")
		return
	}
	var body struct {
		Summary    string  `json:"summary"`
		SourceType *string `json:"source_type"`
		SourceID   *int64  `json:"source_id"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "unreadable body")
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid JSON")
		return
	}
	input := appgoals.RecordInput{Summary: body.Summary, SourceID: body.SourceID}
	if body.SourceType != nil && strings.TrimSpace(*body.SourceType) != "" {
		sourceType := domaingoal.TargetType(*body.SourceType)
		if !sourceType.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid source type")
			return
		}
		input.SourceType = &sourceType
	}
	view, err := h.services.Goals.Record(r.Context(), id, input)
	if err != nil {
		h.writeGoalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newGoalBody(view))
}

// writeGoalError maps goal service errors to problem responses.
func (h *Handler) writeGoalError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appgoals.ErrNotFound),
		errors.Is(err, appgoals.ErrMilestoneNotFound),
		errors.Is(err, appgoals.ErrLinkNotFound),
		errors.Is(err, appgoals.ErrProposalNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", err.Error())
	case errors.Is(err, appgoals.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusConflict, "conflict", "Conflict", err.Error())
	case errors.Is(err, appgoals.ErrInvalidTransition),
		errors.Is(err, appgoals.ErrGoalNotProposed),
		errors.Is(err, appgoals.ErrGoalTerminal),
		errors.Is(err, appgoals.ErrNoPendingProposal),
		errors.Is(err, appgoals.ErrAlreadyProposed),
		errors.Is(err, appgoals.ErrEmptyPayload),
		errors.Is(err, appgoals.ErrMilestoneOutside),
		errors.Is(err, appgoals.ErrProposalNotAllowed):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "precondition-failed", "Precondition Failed", err.Error())
	case errors.Is(err, appgoals.ErrInvalidTarget):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case errors.Is(err, domaingoal.ErrInvalidTitle),
		errors.Is(err, domaingoal.ErrInvalidDescription),
		errors.Is(err, domaingoal.ErrInvalidCriteria),
		errors.Is(err, domaingoal.ErrInvalidMilestone),
		errors.Is(err, domaingoal.ErrInvalidStatus),
		errors.Is(err, domaingoal.ErrInvalidMileStatus),
		errors.Is(err, domaingoal.ErrInvalidLink),
		errors.Is(err, domaingoal.ErrInvalidRelation),
		errors.Is(err, domaingoal.ErrInvalidSummary),
		errors.Is(err, domaingoal.ErrInvalidProgress),
		errors.Is(err, domaingoal.ErrInvalidPayload):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "goal operation failed")
	}
}
