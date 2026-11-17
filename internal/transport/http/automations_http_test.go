package httptransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createAutomationForTest drafts a daily automation with two steps.
func createAutomationForTest(t *testing.T, handler *Handler, title string) automationBody {
	t.Helper()
	payload := fmt.Sprintf(`{
		"title": %q,
		"description": "Каждое утро",
		"expected_result": "Напоминание во входящих",
		"consequences": "Создаёт входящие записи",
		"trigger": {"kind": "schedule", "schedule": {"frequency": "daily", "time": "09:00"}},
		"steps": [
			{"name": "list_tasks", "arguments": {"status": "open"}, "description": "Открытые задачи"},
			{"name": "capture_inbox", "arguments": {"text": "Сводка"}, "description": "Сводка во входящие"}
		]
	}`, title)
	w := automationRequest(t, handler, http.MethodPost, "/api/v1/automations", payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("create automation: status %d body %s", w.Code, w.Body.String())
	}
	var created automationBody
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return created
}

func automationRequest(t *testing.T, handler *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	return recorder
}

func TestAutomationLifecycleOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	created := createAutomationForTest(t, handler, "Утренняя сводка")
	if created.Status != "proposed" || created.Version != 1 {
		t.Fatalf("created = %+v", created)
	}
	if len(created.Steps) != 2 || created.Steps[0].Name != "list_tasks" {
		t.Fatalf("steps = %+v", created.Steps)
	}

	// A manual run is refused while the draft awaits approval.
	w := automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/run", created.ID), "")
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("manual run before approval: status %d body %s", w.Code, w.Body.String())
	}

	// Approval activates and computes the schedule bookmark.
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/approve", created.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("approve: status %d body %s", w.Code, w.Body.String())
	}
	var active automationBody
	if err := json.Unmarshal(w.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if active.Status != "active" || active.NextRunAt == nil {
		t.Fatalf("approved = %+v", active)
	}

	// A scenario change on an active automation needs a pause first.
	renamed := "Утренняя сводка v2"
	patch := fmt.Sprintf(`{"title":%q,"expected_version":%d}`, renamed, active.Version)
	w = automationRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/automations/%d", created.ID), patch)
	if w.Code != http.StatusOK {
		t.Fatalf("text patch: status %d body %s", w.Code, w.Body.String())
	}

	triggerEdit := fmt.Sprintf(`{"trigger":{"kind":"schedule","schedule":{"frequency":"daily","time":"11:00"}},"expected_version":%d}`, active.Version+1)
	w = automationRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/automations/%d", created.ID), triggerEdit)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("trigger patch on active: status %d body %s", w.Code, w.Body.String())
	}

	// Pause, then the scenario edit goes through.
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/status", created.ID), `{"status":"paused"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pause: status %d body %s", w.Code, w.Body.String())
	}
	var paused automationBody
	w = automationRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/automations/%d", created.ID), "")
	if err := json.Unmarshal(w.Body.Bytes(), &paused); err != nil {
		t.Fatalf("decode paused: %v", err)
	}
	triggerEdit = fmt.Sprintf(`{"trigger":{"kind":"schedule","schedule":{"frequency":"daily","time":"11:00"}},"expected_version":%d}`, paused.Version)
	w = automationRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/automations/%d", created.ID), triggerEdit)
	if w.Code != http.StatusOK {
		t.Fatalf("trigger patch while paused: status %d body %s", w.Code, w.Body.String())
	}

	// Resume runs from the new schedule.
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/status", created.ID), `{"status":"active"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("resume: status %d", w.Code)
	}

	// A manual run on the approved automation journals real steps.
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/run", created.ID), "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("manual run: status %d body %s", w.Code, w.Body.String())
	}
	w = automationRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/automations/%d/runs", created.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("runs: status %d", w.Code)
	}
	var runs struct {
		Data []automationRunBody `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if runs.Meta.Total != 1 || len(runs.Data) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	if runs.Data[0].Status != "succeeded" || len(runs.Data[0].Steps) != 2 {
		t.Fatalf("run = %+v", runs.Data[0])
	}
	for _, step := range runs.Data[0].Steps {
		if step.Status != "succeeded" {
			t.Fatalf("step %q status = %s", step.Name, step.Status)
		}
	}

	// Single run retrieval.
	w = automationRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/automations/%d/runs/%d", created.ID, runs.Data[0].ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("run detail: status %d", w.Code)
	}

	// Unknown tools are refused at design time.
	broken := `{"title":"Сломано","trigger":{"kind":"event","event":{"name":"task.completed"}},
		"steps":[{"name":"run_command"}]}`
	w = automationRequest(t, handler, http.MethodPost, "/api/v1/automations", broken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("infrastructure step: status %d body %s", w.Code, w.Body.String())
	}

	// Archive and then delete.
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/status", created.ID), `{"status":"archived"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("archive: status %d", w.Code)
	}
	w = automationRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/v1/automations/%d/run", created.ID), "")
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("archived run: status %d", w.Code)
	}
	w = automationRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/v1/automations/%d", created.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status %d", w.Code)
	}
	w = automationRequest(t, handler, http.MethodGet, fmt.Sprintf("/api/v1/automations/%d", created.ID), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted read: status %d", w.Code)
	}
}

func TestAutomationListFilterOverHTTP(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	createAutomationForTest(t, handler, "Первая")
	createAutomationForTest(t, handler, "Вторая")

	w := automationRequest(t, handler, http.MethodGet, "/api/v1/automations?status=proposed", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var list struct {
		Data []automationBody `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 2 {
		t.Fatalf("total = %d, want 2", list.Meta.Total)
	}
	if len(list.Data) == 0 || !strings.Contains(string(list.Data[0].Trigger), `"kind":"schedule"`) {
		t.Fatalf("trigger not rendered: %s", list.Data[0].Trigger)
	}
}

// TestAutomationRejectsRunLifecycleEventTrigger pins the structural cycle
// guard at the API boundary: automation.run.* events are published for every
// run — including a depth-refused skip — so they can never be triggers.
func TestAutomationRejectsRunLifecycleEventTrigger(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	payload := `{
		"title": "После каждого запуска",
		"expected_result": "Реакция на журнал",
		"trigger": {"kind": "event", "event": {"name": "automation.run.finished"}},
		"steps": [
			{"name": "list_tasks", "arguments": {"status": "open"}, "description": "Открытые задачи"}
		]
	}`
	w := automationRequest(t, handler, http.MethodPost, "/api/v1/automations", payload)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
