package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

type planItemBody struct {
	BlockID    int64  `json:"block_id,omitempty"`
	TaskID     int64  `json:"task_id"`
	Title      string `json:"title"`
	Priority   string `json:"priority"`
	Start      string `json:"start"`
	End        string `json:"end"`
	StartLocal string `json:"start_local"`
	EndLocal   string `json:"end_local"`
	Source     string `json:"source,omitempty"`
	Status     string `json:"status,omitempty"`
	Estimate   int    `json:"estimate_minutes,omitempty"`
	Recurring  bool   `json:"recurring,omitempty"`
	DueAt      string `json:"due_at,omitempty"`
	DueDate    string `json:"due_date,omitempty"`
}

func newPlanItemBody(item planning.PlanItem, loc *time.Location) planItemBody {
	body := planItemBody{
		BlockID:    item.BlockID,
		TaskID:     item.TaskID,
		Title:      item.Title,
		Priority:   string(item.Priority),
		Start:      formatTime(item.Start),
		End:        formatTime(item.End),
		StartLocal: item.Start.In(loc).Format("15:04"),
		EndLocal:   item.End.In(loc).Format("15:04"),
		Source:     string(item.Source),
		Status:     string(item.Status),
		Estimate:   item.Estimate,
		Recurring:  item.Recurring,
		DueDate:    item.DueDate,
	}
	if item.DueAt != nil {
		body.DueAt = formatTime(*item.DueAt)
	}
	return body
}

// getDayPlan handles GET /api/v1/plan/day?date=YYYY-MM-DD.
//
// The response contains what the day already holds (appointments and blocks) and
// a proposal for the rest of the free time. Nothing is written: the plan exists
// only when the user accepts it.
func (h *Handler) getDayPlan(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("date"))
	date, err := task.ParseCalendarDate(raw)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "date must be YYYY-MM-DD")
		return
	}

	plan, err := h.services.Planning.PlanDay(r.Context(), date)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}

	loc := h.services.Planning.Location()
	blocks := make([]planItemBody, 0, len(plan.Blocks))
	for _, item := range plan.Blocks {
		blocks = append(blocks, newPlanItemBody(item, loc))
	}
	proposed := make([]planItemBody, 0, len(plan.Proposed))
	for _, item := range plan.Proposed {
		proposed = append(proposed, newPlanItemBody(item, loc))
	}

	busy := make([]map[string]any, 0, len(plan.Busy))
	for _, item := range plan.Busy {
		busy = append(busy, map[string]any{
			"label":   item.Label,
			"kind":    item.Kind,
			"start":   formatTime(item.Start),
			"end":     formatTime(item.End),
			"task_id": item.TaskID,
		})
	}
	unplaced := make([]map[string]any, 0, len(plan.Unplaced))
	for _, item := range plan.Unplaced {
		unplaced = append(unplaced, map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"estimate_minutes": item.EstimateMin,
			"reason":           item.Reason,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"date":            plan.Date,
		"working":         plan.Working,
		"workday_start":   plan.WorkdayStart,
		"workday_end":     plan.WorkdayEnd,
		"free_minutes":    plan.FreeMinutes,
		"planned_minutes": plan.PlannedMinutes,
		"blocks":          blocks,
		"proposed":        proposed,
		"busy":            busy,
		"unplaced":        unplaced,
	})
}

// getPlanProposal handles GET /api/v1/plan/replan?days=N.
func (h *Handler) getPlanProposal(w http.ResponseWriter, r *http.Request) {
	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 30 {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "days must be 1-30")
			return
		}
		days = parsed
	}

	proposal, err := h.services.Planning.Replan(r.Context(), days)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}

	moves := make([]map[string]any, 0, len(proposal.Moves))
	for _, item := range proposal.Moves {
		entry := map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"to":               item.To,
			"reason":           item.Reason,
			"estimate_minutes": item.Estimate,
		}
		if item.From != "" {
			entry["from"] = item.From
		}
		moves = append(moves, entry)
	}
	placements := make([]map[string]any, 0, len(proposal.Placements))
	for _, item := range proposal.Placements {
		placements = append(placements, map[string]any{
			"task_id": item.TaskID,
			"date":    item.Date,
			"start":   formatTime(item.Start),
			"end":     formatTime(item.End),
		})
	}
	unplaced := make([]map[string]any, 0, len(proposal.Unplaced))
	for _, item := range proposal.Unplaced {
		unplaced = append(unplaced, map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"estimate_minutes": item.EstimateMin,
			"reason":           item.Reason,
		})
	}
	days = len(proposal.Days)
	load := make([]map[string]any, 0, days)
	for _, item := range proposal.Days {
		load = append(load, map[string]any{
			"date":            item.Date,
			"working":         item.Working,
			"free_minutes":    item.FreeMinutes,
			"planned_minutes": item.PlannedMin,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": formatTime(proposal.GeneratedAt),
		"horizon_days": proposal.HorizonDays,
		"summary":      proposal.Summary,
		"moves":        moves,
		"placements":   placements,
		"unplaced":     unplaced,
		"days":         load,
	})
}

// applyPlan handles POST /api/v1/plan/apply.
func (h *Handler) applyPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReplaceDates []string `json:"replace_dates"`
		Items        []struct {
			TaskID int64  `json:"task_id"`
			Start  string `json:"start"`
			End    string `json:"end"`
		} `json:"items"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}

	in := planning.ApplyInput{ReplaceDates: body.ReplaceDates}
	for _, item := range body.Items {
		start, err := time.Parse(time.RFC3339, item.Start)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "items.start must be RFC3339")
			return
		}
		end, err := time.Parse(time.RFC3339, item.End)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "items.end must be RFC3339")
			return
		}
		in.Items = append(in.Items, planning.PlacementInput{TaskID: item.TaskID, Start: start, End: end})
	}

	result, err := h.services.Planning.Apply(r.Context(), in)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}

	loc := h.services.Planning.Location()
	applied := make([]planItemBody, 0, len(result.Applied))
	for _, item := range result.Applied {
		applied = append(applied, newPlanItemBody(item, loc))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"applied": applied,
		"skipped": result.Skipped,
	})
}

// getTaskReminders handles GET /api/v1/tasks/{id}/reminders.
func (h *Handler) getTaskReminders(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	reminders, err := h.services.Planning.RemindersOf(r.Context(), id)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(reminders))
	for _, item := range reminders {
		entry := map[string]any{
			"id":      item.ID,
			"kind":    string(item.Kind),
			"status":  string(item.Status),
			"fire_at": formatTime(item.FireAt),
		}
		if item.SentAt != nil {
			entry["sent_at"] = formatTime(*item.SentAt)
		}
		data = append(data, entry)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// deletePlanBlock handles DELETE /api/v1/plan/blocks/{id}.
//
// Removing a block removes the reserved time, not the task: the work simply
// becomes unplanned again.
func (h *Handler) deletePlanBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.services.Planning.RemoveBlock(r.Context(), id); err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// estimateTask handles GET /api/v1/tasks/estimate?title=…&tags=a,b
//
// It answers "how long does this take" from the user's own history, which is what
// makes planning realistic instead of optimistic.
func (h *Handler) estimateTask(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if title == "" {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "title is required")
		return
	}
	tags := splitList(r.URL.Query().Get("tags"))
	suggestion, err := h.services.Planning.SuggestEstimate(r.Context(), title, tags)
	if err != nil {
		h.writePlanningError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"minutes":  suggestion.Minutes,
		"source":   suggestion.Source,
		"samples":  suggestion.Samples,
		"based_on": suggestion.BasedOn,
		"question": suggestion.Question,
	})
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// writePlanningError maps planner errors onto HTTP problems.
func (h *Handler) writePlanningError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, planning.ErrNoTask):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", err.Error())
	case errors.Is(err, planning.ErrTaskNotOpen),
		errors.Is(err, planning.ErrPlacementInvalid),
		errors.Is(err, planning.ErrPlacementOutsideWindow):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case errors.Is(err, planning.ErrPlacementConflict):
		h.writeProblem(w, r, http.StatusConflict, "plan-conflict", "Schedule Conflict", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal-error", "Internal Error", err.Error())
	}
}

// decodeRepeatArg is shared by the settings endpoint and the assistant tools.
func decodeRepeatArg(raw json.RawMessage) (*task.Repeat, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var repeat task.Repeat
	if err := json.Unmarshal(raw, &repeat); err != nil {
		return nil, err
	}
	return &repeat, nil
}
