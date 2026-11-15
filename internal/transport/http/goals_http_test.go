package httptransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func goalRequest(t *testing.T, handler *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	return recorder
}

// createGoalForTest creates a proposed goal with two milestones.
func createGoalForTest(t *testing.T, handler *Handler, title string) goalBody {
	t.Helper()
	payload := fmt.Sprintf(`{"title":%q,"description":"Описание цели","milestones":[{"title":"Этап 1","criteria":"критерий 1"},{"title":"Этап 2","criteria":"критерий 2"}]}`, title)
	w := goalRequest(t, handler, http.MethodPost, "/api/v1/goals", payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("create goal: status %d body %s", w.Code, w.Body.String())
	}
	var created goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return created
}

func TestGoalLifecycleOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	created := createGoalForTest(t, handler, "Запустить личный блог")
	if created.Status != "proposed" || created.Version != 1 {
		t.Fatalf("created = %+v", created)
	}
	if len(created.Milestones) != 2 || created.Milestones[0].Status != "pending" {
		t.Fatalf("milestones = %+v", created.Milestones)
	}

	// List by status.
	w := goalRequest(t, handler, http.MethodGet, "/api/v1/goals?status=proposed", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var list struct {
		Data []goalBody `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 1 {
		t.Fatalf("list total = %d", list.Meta.Total)
	}

	// Approve starts organized work.
	w = goalRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/goals/%d/approve", created.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("approve: status %d body %s", w.Code, w.Body.String())
	}
	var approved goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if approved.Status != "active" || approved.ApprovedAt == nil {
		t.Fatalf("approved = %+v", approved)
	}

	// Milestone progress.
	milestoneID := approved.Milestones[0].ID
	payload := fmt.Sprintf(`{"status":"done","expected_version":%d}`, approved.Milestones[0].Version)
	w = goalRequest(t, handler, http.MethodPatch,
		fmt.Sprintf("/api/v1/goals/%d/milestones/%d", created.ID, milestoneID), payload)
	if w.Code != http.StatusOK {
		t.Fatalf("patch milestone: status %d body %s", w.Code, w.Body.String())
	}
	var withProgress goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &withProgress); err != nil {
		t.Fatalf("decode milestone patch: %v", err)
	}
	if withProgress.Progress == nil || withProgress.Progress.MilestonesDone != 1 || withProgress.Progress.Percent == nil || *withProgress.Progress.Percent != 50 {
		t.Fatalf("progress = %+v", withProgress.Progress)
	}
	if withProgress.Signals == nil || !withProgress.Signals.Stalled {
		// The test clock does not move, so the journal is fresh: stalled must be false.
		if withProgress.Signals != nil && withProgress.Signals.Stalled {
			t.Fatal("fresh goal must not be stalled")
		}
	}
	if len(withProgress.ProgressLog) == 0 {
		t.Fatal("milestone completion must reach the progress journal")
	}

	// A proposal on the approved goal waits for the user.
	w = goalRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/goals/%d/proposals", created.ID),
		`{"summary":"Отказаться от второго этапа","remove_milestone_ids":[`+strconv.FormatInt(approved.Milestones[1].ID, 10)+`]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create proposal: status %d body %s", w.Code, w.Body.String())
	}
	var proposalView struct {
		Proposal struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &proposalView); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	if proposalView.Proposal.Status != "pending" {
		t.Fatalf("proposal = %+v", proposalView.Proposal)
	}

	w = goalRequest(t, handler, http.MethodPost,
		fmt.Sprintf("/api/v1/goals/%d/proposals/%d/apply", created.ID, proposalView.Proposal.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("apply proposal: status %d body %s", w.Code, w.Body.String())
	}
	var applied goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if len(applied.Milestones) != 1 {
		t.Fatalf("applied proposal must remove the milestone, milestones = %+v", applied.Milestones)
	}

	// Invalid transition is a precondition failure.
	w = goalRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/goals/%d/approve", created.ID), "")
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("second approve status = %d, want 412", w.Code)
	}

	// Pause, then achieve.
	w = goalRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/goals/%d/status", created.ID), `{"status":"paused"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pause: status %d body %s", w.Code, w.Body.String())
	}
	w = goalRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/goals/%d/status", created.ID), `{"status":"achieved"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("achieve: status %d body %s", w.Code, w.Body.String())
	}

	// Delete.
	w = goalRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/goals/%d", created.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status %d body %s", w.Code, w.Body.String())
	}
	if w = goalRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/goals/%d", created.ID), ""); w.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d, want 404", w.Code)
	}
}

func TestGoalValidationAndProposedPlanOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Missing title is a validation failure.
	if w := goalRequest(t, handler, http.MethodPost, "/api/v1/goals", `{"title":""}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty title status = %d, want 400", w.Code)
	}

	created := createGoalForTest(t, handler, "Спланировать переезд")
	goalID := strconv.FormatInt(created.ID, 10)

	// A proposed plan may still be edited directly.
	w := goalRequest(t, handler, http.MethodPatch, "/api/v1/goals/"+goalID,
		fmt.Sprintf(`{"title":"Переезд в Калининград","target_date":"2027-06-01","expected_version":%d}`, created.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", w.Code, w.Body.String())
	}
	var edited goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &edited); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if edited.Title != "Переезд в Калининград" || edited.TargetDate == nil || *edited.TargetDate != "2027-06-01" {
		t.Fatalf("edited = %+v", edited)
	}

	// Optimistic locking.
	if w := goalRequest(t, handler, http.MethodPatch, "/api/v1/goals/"+goalID, `{"title":"Дубль","expected_version":1}`); w.Code != http.StatusConflict {
		t.Fatalf("stale patch status = %d, want 409", w.Code)
	}

	// Invalid date is refused.
	if w := goalRequest(t, handler, http.MethodPost, "/api/v1/goals/"+goalID+"/milestones", `{"title":"Этап","due_date":"06/2027"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date status = %d, want 400", w.Code)
	}

	// Add and remove a milestone while the plan is unapproved.
	w = goalRequest(t, handler, http.MethodPost, "/api/v1/goals/"+goalID+"/milestones", `{"title":"Найти квартиру"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add milestone: status %d body %s", w.Code, w.Body.String())
	}
	var extended goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &extended); err != nil {
		t.Fatalf("decode add milestone: %v", err)
	}
	if len(extended.Milestones) != 3 {
		t.Fatalf("milestones = %+v", extended.Milestones)
	}
	if w := goalRequest(t, handler, http.MethodDelete,
		fmt.Sprintf("/api/v1/goals/%s/milestones/%d", goalID, extended.Milestones[2].ID), ""); w.Code != http.StatusOK {
		t.Fatalf("delete milestone: status %d body %s", w.Code, w.Body.String())
	}

	// Unknown goal id is 404.
	if w := goalRequest(t, handler, http.MethodGet, "/api/v1/goals/9999", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown goal status = %d, want 404", w.Code)
	}

	// Progress notes reach the journal.
	w = goalRequest(t, handler, http.MethodPost, "/api/v1/goals/"+goalID+"/progress", `{"summary":"Сравнили районы"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("record progress: status %d body %s", w.Code, w.Body.String())
	}
	var withNote goalBody
	if err := json.Unmarshal(w.Body.Bytes(), &withNote); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if len(withNote.ProgressLog) == 0 || withNote.ProgressLog[0].Summary != "Сравнили районы" {
		t.Fatalf("progress log = %+v", withNote.ProgressLog)
	}
}
