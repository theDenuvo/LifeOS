package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

// Roadmap stage 6: user automations.

type automationStepBody struct {
	Position    int             `json:"position"`
	Name        string          `json:"name"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Description string          `json:"description,omitempty"`
}

type automationRunStepBody struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type automationRunBody struct {
	ID         int64                   `json:"id"`
	Trigger    string                  `json:"trigger"`
	Status     string                  `json:"status"`
	Steps      []automationRunStepBody `json:"steps"`
	Summary    string                  `json:"summary"`
	Error      string                  `json:"error,omitempty"`
	StartedAt  string                  `json:"started_at"`
	FinishedAt *string                 `json:"finished_at,omitempty"`
}

type automationBody struct {
	ID             int64                `json:"id"`
	Title          string               `json:"title"`
	Description    string               `json:"description"`
	Status         string               `json:"status"`
	Trigger        json.RawMessage      `json:"trigger"`
	Steps          []automationStepBody `json:"steps"`
	ExpectedResult string               `json:"expected_result"`
	Consequences   string               `json:"consequences"`
	ApprovedAt     *string              `json:"approved_at,omitempty"`
	LastRunAt      *string              `json:"last_run_at,omitempty"`
	NextRunAt      *string              `json:"next_run_at,omitempty"`
	CreatedAt      string               `json:"created_at"`
	UpdatedAt      string               `json:"updated_at"`
	Version        int                  `json:"version"`
	Runs           []automationRunBody  `json:"runs"`
}

func newAutomationBody(view *appautomation.View) automationBody {
	a := view.Automation
	trigger, err := json.Marshal(a.Trigger)
	if err != nil {
		trigger = []byte(`{}`)
	}
	body := automationBody{
		ID:             a.ID,
		Title:          a.Title,
		Description:    a.Description,
		Status:         string(a.Status),
		Trigger:        trigger,
		Steps:          []automationStepBody{},
		ExpectedResult: a.ExpectedResult,
		Consequences:   a.Consequences,
		ApprovedAt:     formatOptionalTime(a.ApprovedAt),
		LastRunAt:      formatOptionalTime(a.LastRunAt),
		NextRunAt:      formatOptionalTime(a.NextRunAt),
		CreatedAt:      formatTime(a.CreatedAt),
		UpdatedAt:      formatTime(a.UpdatedAt),
		Version:        a.Version,
		Runs:           []automationRunBody{},
	}
	for i, step := range a.Steps {
		var arguments json.RawMessage
		if len(step.Arguments) > 0 {
			arguments = step.Arguments
		}
		body.Steps = append(body.Steps, automationStepBody{
			Position: i + 1, Name: step.Name, Arguments: arguments, Description: step.Description,
		})
	}
	for _, run := range view.Runs {
		body.Runs = append(body.Runs, newAutomationRunBody(run))
	}
	return body
}

func newAutomationRunBody(run *domainautomation.Run) automationRunBody {
	body := automationRunBody{
		ID:         run.ID,
		Trigger:    run.Trigger,
		Status:     string(run.Status),
		Steps:      []automationRunStepBody{},
		Summary:    run.Summary,
		Error:      run.Error,
		StartedAt:  formatTime(run.StartedAt),
		FinishedAt: formatOptionalTime(run.FinishedAt),
	}
	for _, step := range run.Steps {
		body.Steps = append(body.Steps, automationRunStepBody{
			Name: step.Name, Status: string(step.Status),
			Output: step.Output, Error: step.Error, DurationMS: step.DurationMS,
		})
	}
	return body
}

// listAutomations handles GET /api/v1/automations.
func (h *Handler) listAutomations(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	filter := appautomation.Filter{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := domainautomation.Status(raw)
		if !status.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
			return
		}
		filter.Status = &status
	}
	filter.Limit = queryInt(r, "limit", 30)
	filter.Offset = queryInt(r, "offset", 0)
	views, total, err := h.services.Automations.List(r.Context(), filter)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	data := make([]automationBody, 0, len(views))
	for _, view := range views {
		data = append(data, newAutomationBody(view))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": filter.Limit, "offset": filter.Offset, "total": total},
	})
}

// createAutomation handles POST /api/v1/automations. The created automation
// always starts proposed: enabling it is a separate user decision.
func (h *Handler) createAutomation(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	input, err := decodeAutomationPayload(r)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	view, err := h.services.Automations.Create(r.Context(), *input)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newAutomationBody(view))
}

func decodeAutomationPayload(r *http.Request) (*appautomation.CreateInput, error) {
	var body struct {
		Title          string          `json:"title"`
		Description    string          `json:"description"`
		ExpectedResult string          `json:"expected_result"`
		Consequences   string          `json:"consequences"`
		Trigger        json.RawMessage `json:"trigger"`
		Steps          []struct {
			Name        string          `json:"name"`
			Arguments   json.RawMessage `json:"arguments"`
			Description string          `json:"description"`
		} `json:"steps"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, errors.New("unreadable body")
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, errors.New("invalid JSON")
	}
	var trigger domainautomation.Trigger
	if err := json.Unmarshal(body.Trigger, &trigger); err != nil {
		return nil, errors.New("trigger must be a valid object")
	}
	steps := make([]domainautomation.Step, 0, len(body.Steps))
	for _, step := range body.Steps {
		steps = append(steps, domainautomation.Step{
			Name: step.Name, Arguments: step.Arguments, Description: step.Description,
		})
	}
	return &appautomation.CreateInput{
		Title: body.Title, Description: body.Description,
		Trigger: trigger, Steps: steps,
		ExpectedResult: body.ExpectedResult, Consequences: body.Consequences,
	}, nil
}

func (h *Handler) automationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid automation id")
		return 0, false
	}
	return id, true
}

// getAutomation handles GET /api/v1/automations/{id}.
func (h *Handler) getAutomation(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Automations.Get(r.Context(), id)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newAutomationBody(view))
}

// patchAutomation handles PATCH /api/v1/automations/{id}.
func (h *Handler) patchAutomation(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	var body struct {
		Title          *string          `json:"title"`
		Description    *string          `json:"description"`
		ExpectedResult *string          `json:"expected_result"`
		Consequences   *string          `json:"consequences"`
		Trigger        *json.RawMessage `json:"trigger"`
		Steps          *[]struct {
			Name        string          `json:"name"`
			Arguments   json.RawMessage `json:"arguments"`
			Description string          `json:"description"`
		} `json:"steps"`
		ExpectedVersion int `json:"expected_version"`
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
	input := appautomation.UpdateInput{
		Title: body.Title, Description: body.Description,
		ExpectedResult: body.ExpectedResult, Consequences: body.Consequences,
	}
	if body.Trigger != nil {
		var trigger domainautomation.Trigger
		if err := json.Unmarshal(*body.Trigger, &trigger); err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "trigger must be a valid object")
			return
		}
		input.Trigger = &trigger
	}
	if body.Steps != nil {
		steps := make([]domainautomation.Step, 0, len(*body.Steps))
		for _, step := range *body.Steps {
			steps = append(steps, domainautomation.Step{
				Name: step.Name, Arguments: step.Arguments, Description: step.Description,
			})
		}
		input.Steps = steps
	}
	view, err := h.services.Automations.Update(r.Context(), id, body.ExpectedVersion, input)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newAutomationBody(view))
}

// deleteAutomation handles DELETE /api/v1/automations/{id}.
func (h *Handler) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	if err := h.services.Automations.Delete(r.Context(), id); err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// approveAutomation handles POST /api/v1/automations/{id}/approve.
func (h *Handler) approveAutomation(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Automations.Approve(r.Context(), id)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newAutomationBody(view))
}

// changeAutomationStatus handles POST /api/v1/automations/{id}/status.
func (h *Handler) changeAutomationStatus(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
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
	status := domainautomation.Status(body.Status)
	if !status.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
		return
	}
	view, err := h.services.Automations.SetStatus(r.Context(), id, status)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newAutomationBody(view))
}

// runAutomationNow handles POST /api/v1/automations/{id}/run.
func (h *Handler) runAutomationNow(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Automations.RunNow(r.Context(), id)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, newAutomationBody(view))
}

// listAutomationRuns handles GET /api/v1/automations/{id}/runs.
func (h *Handler) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	runs, total, err := h.services.Automations.Runs(r.Context(), id, queryInt(r, "limit", 20), queryInt(r, "offset", 0))
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	data := make([]automationRunBody, 0, len(runs))
	for _, run := range runs {
		data = append(data, newAutomationRunBody(run))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"total": total},
	})
}

// getAutomationRun handles GET /api/v1/automations/{id}/runs/{runId}.
func (h *Handler) getAutomationRun(w http.ResponseWriter, r *http.Request) {
	if h.services.Automations == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Automations are not configured")
		return
	}
	id, ok := h.automationID(w, r)
	if !ok {
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("runId"), 10, 64)
	if err != nil || runID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid run id")
		return
	}
	run, err := h.services.Automations.Run(r.Context(), id, runID)
	if err != nil {
		h.writeAutomationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newAutomationRunBody(run))
}

func (h *Handler) writeAutomationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appautomation.ErrNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "automation not found")
	case errors.Is(err, appautomation.ErrRunNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "run not found")
	case errors.Is(err, appautomation.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusConflict, "version-conflict", "Version Conflict", err.Error())
	case errors.Is(err, appautomation.ErrInvalidTransition):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "invalid-transition", "Invalid Transition", err.Error())
	case errors.Is(err, appautomation.ErrNotExecutable):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "not-executable", "Not Executable", err.Error())
	case errors.Is(err, appautomation.ErrStepNotAllowed):
		h.writeProblem(w, r, http.StatusUnprocessableEntity, "step-not-allowed", "Step Not Allowed", err.Error())
	case errors.Is(err, appautomation.ErrAlreadyRunning):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "already-running", "Already Running", err.Error())
	default:
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	}
}
