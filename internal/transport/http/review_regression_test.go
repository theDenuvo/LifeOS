package httptransport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// ---------------------------------------------------------------------------
// P-06: a failed next occurrence is not lost
// ---------------------------------------------------------------------------

func TestRecurrenceIsRecoveredAfterAFailedCreation(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	id, version := createTask(t, stack, map[string]any{
		"title":        "Зарядка",
		"planned_date": "2026-11-09",
		"repeat":       map[string]any{"freq": "daily", "interval": 1, "window_days": 1},
	})

	// The creation fails: the completion is committed, the continuation is not.
	stack.tasks.SetRecurrenceHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return errors.New("storage is busy")
	})
	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/complete", nil,
		map[string]string{"If-Match": `"` + itoa(version) + `"`})
	if recorder.Code != 200 {
		t.Fatalf("complete: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if count := occurrenceCount(t, stack); count != 0 {
		t.Fatalf("occurrences = %d, want the failure to leave none", count)
	}

	// The missing continuation is visible instead of silently lost.
	recorder, pending := apiRequest(t, stack.handler, "GET", "/api/v1/recurrence/pending", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("pending: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if data, _ := pending["data"].([]any); len(data) != 1 {
		t.Fatalf("pending = %+v, want the interrupted recurrence", pending["data"])
	}

	// After the temporary failure the retry creates exactly one occurrence.
	stack.tasks.SetRecurrenceHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return stack.recurrence.Next(ctx, completed)
	})
	recorder, result := apiRequest(t, stack.handler, "POST", "/api/v1/recurrence/reconcile", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("reconcile: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if result["created"] != float64(1) {
		t.Fatalf("created = %v, want 1", result["created"])
	}
	if count := occurrenceCount(t, stack); count != 1 {
		t.Fatalf("occurrences = %d, want 1", count)
	}

	// Running it again must not duplicate anything.
	recorder, result = apiRequest(t, stack.handler, "POST", "/api/v1/recurrence/reconcile", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("second reconcile: status %d", recorder.Code)
	}
	if count := occurrenceCount(t, stack); count != 1 {
		t.Fatalf("occurrences after a repeated reconcile = %d, want 1", count)
	}

	// The completed task stays completed and the child points back at it.
	var status, parentID string
	if err := stack.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if status != "completed" {
		t.Fatalf("parent status = %q, want completed", status)
	}
	if err := stack.db.QueryRow(`
		SELECT CAST(repeat_parent_id AS TEXT) FROM tasks WHERE repeat_parent_id = ?`, id).Scan(&parentID); err != nil {
		t.Fatalf("read child: %v", err)
	}
	if parentID != itoa(id) {
		t.Fatalf("repeat_parent_id = %q, want %d", parentID, id)
	}
}

func TestRecurrenceIsRecoveredAfterARestart(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	first := newPlanningStack(t, now)

	id, version := createTask(t, first, map[string]any{
		"title":        "Дневник",
		"planned_date": "2026-11-09",
		"repeat":       map[string]any{"freq": "daily", "interval": 1},
	})
	first.tasks.SetRecurrenceHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return errors.New("process is going down")
	})
	recorder, _ := apiRequest(t, first.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/complete", nil,
		map[string]string{"If-Match": `"` + itoa(version) + `"`})
	if recorder.Code != 200 {
		t.Fatalf("complete: status %d", recorder.Code)
	}
	path := first.path
	first.cleanup() // the process ends before the retry happened

	// A fresh process opens the same database and finds the unfinished work.
	second := newPlanningStackAt(t, now, path)
	defer second.cleanup()
	recorder, result := apiRequest(t, second.handler, "POST", "/api/v1/recurrence/reconcile", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("reconcile: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if result["created"] != float64(1) {
		t.Fatalf("created = %v, want the missing occurrence", result["created"])
	}
	if count := occurrenceCount(t, second); count != 1 {
		t.Fatalf("occurrences after restart = %d, want 1", count)
	}
}

func TestProcessingACompletionTwiceCreatesOneOccurrence(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	id, version := createTask(t, stack, map[string]any{
		"title":        "Растяжка",
		"planned_date": "2026-11-09",
		"repeat":       map[string]any{"freq": "daily", "interval": 1},
	})
	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/complete", nil,
		map[string]string{"If-Match": `"` + itoa(version) + `"`})
	if recorder.Code != 200 {
		t.Fatalf("complete: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	completed, err := stack.tasks.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("reload completed task: %v", err)
	}
	// The same completion event is processed again, as a retry would do.
	if err := stack.recurrence.Next(context.Background(), completed); err != nil {
		t.Fatalf("repeat next: %v", err)
	}
	if count := occurrenceCount(t, stack); count != 1 {
		t.Fatalf("occurrences = %d, want exactly one", count)
	}
}

// occurrenceCount returns how many tasks are continuations of another task.
func occurrenceCount(t *testing.T, stack *planningStack) int {
	t.Helper()
	var count int
	if err := stack.db.QueryRow(`SELECT COUNT(1) FROM tasks WHERE repeat_parent_id IS NOT NULL`).Scan(&count); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// P-07: reminders are durable, not best effort
// ---------------------------------------------------------------------------

func TestReminderWaitsForTheClientInsteadOfBeingMarkedSent(t *testing.T) {
	now := moscow(t, "2026-11-09 09:50")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	id, _ := createTask(t, stack, map[string]any{
		"title":    "Сдать отчёт",
		"priority": "high",
		"due_at":   moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
	})

	// No client is connected when the moment comes. The live event goes out into a
	// broker nobody listens to — that must not count as delivery.
	stack.clock.Advance(12 * time.Minute)
	dispatcher := planning.NewDispatcher(stack.planner, events.NewBroker(), stack.clock, time.Minute)
	if err := dispatcher.Tick(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	recorder, due := apiRequest(t, stack.handler, "GET", "/api/v1/reminders", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("reminders: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	data, _ := due["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("due reminders = %+v, want the one that fired while nobody was listening", due["data"])
	}
	item, _ := data[0].(map[string]any)
	if item["delivered"] != false {
		t.Fatalf("delivered = %v, want false until the client acknowledges", item["delivered"])
	}
	if int64(item["task_id"].(float64)) != id {
		t.Fatalf("reminder for task %v, want %d", item["task_id"], id)
	}

	// Acknowledging is what records delivery, and it is idempotent.
	reminderID := int64(item["reminder_id"].(float64))
	for i := 0; i < 2; i++ {
		recorder, _ = apiRequest(t, stack.handler, "POST", "/api/v1/reminders/"+itoa(reminderID)+"/ack", nil, nil)
		if recorder.Code != 204 {
			t.Fatalf("ack (pass %d): status %d, body %s", i+1, recorder.Code, recorder.Body.String())
		}
	}
	_, due = apiRequest(t, stack.handler, "GET", "/api/v1/reminders", nil, nil)
	if data, _ := due["data"].([]any); len(data) != 0 {
		t.Fatalf("acknowledged reminder is still offered: %+v", data)
	}
	var status string
	if err := stack.db.QueryRow(`SELECT status FROM task_reminders WHERE id = ?`, reminderID).Scan(&status); err != nil {
		t.Fatalf("read reminder: %v", err)
	}
	if status != "sent" {
		t.Fatalf("status = %q, want sent after acknowledgement", status)
	}

	// A further dispatch must not resurrect it.
	if err := dispatcher.Tick(context.Background()); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	_, due = apiRequest(t, stack.handler, "GET", "/api/v1/reminders", nil, nil)
	if data, _ := due["data"].([]any); len(data) != 0 {
		t.Fatalf("the reminder reappeared after acknowledgement: %+v", data)
	}
}

func TestReminderSurvivesAnApplicationRestart(t *testing.T) {
	now := moscow(t, "2026-11-09 09:50")
	stack := newPlanningStack(t, now)

	createTask(t, stack, map[string]any{
		"title":    "Позвонить клиенту",
		"priority": "high",
		"due_at":   moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
	})

	// The reminder fires, nobody is connected, and then the process stops.
	stack.clock.Advance(15 * time.Minute)
	dispatcher := planning.NewDispatcher(stack.planner, events.NewBroker(), stack.clock, time.Minute)
	if err := dispatcher.Tick(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	path := stack.path
	restartAt := stack.clock.Now()
	stack.cleanup()

	// A new process reads the same database: the reminder is still pending.
	restarted := newPlanningStackAt(t, restartAt, path)
	defer restarted.cleanup()
	recorder, due := apiRequest(t, restarted.handler, "GET", "/api/v1/reminders", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("reminders: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if data, _ := due["data"].([]any); len(data) != 1 {
		t.Fatalf("reminders after restart = %+v, want the pending one", due["data"])
	}
}

func TestReminderScheduleIsRepairedAfterAFailedSync(t *testing.T) {
	now := moscow(t, "2026-11-09 09:50")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	createTask(t, stack, map[string]any{
		"title":    "Проверить счета",
		"priority": "high",
		"due_at":   moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
	})

	// Simulate a reminder regeneration that failed after a task change: the task
	// is fine, its notification schedule is gone.
	if _, err := stack.db.Exec(`DELETE FROM task_reminders`); err != nil {
		t.Fatalf("drop reminders: %v", err)
	}
	stack.clock.Advance(20 * time.Minute)
	_, due := apiRequest(t, stack.handler, "GET", "/api/v1/reminders", nil, nil)
	if data, _ := due["data"].([]any); len(data) != 0 {
		t.Fatalf("reminders = %+v, want none before the repair", data)
	}

	recorder, result := apiRequest(t, stack.handler, "POST", "/api/v1/reminders/resync", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("resync: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if repaired, _ := result["repaired"].(float64); repaired < 1 {
		t.Fatalf("repaired = %v, want at least one task", result["repaired"])
	}
	// The moment that already passed is not resurrected; the remaining schedule is
	// rebuilt, so the follow-up after the deadline is delivered when it comes.
	stack.clock.Advance(2 * time.Hour)
	_, due = apiRequest(t, stack.handler, "GET", "/api/v1/reminders", nil, nil)
	if data, _ := due["data"].([]any); len(data) != 1 {
		t.Fatalf("reminders after repair = %+v, want the repaired follow-up", due["data"])
	}
}

// ---------------------------------------------------------------------------
// P-09: completion checks over the real API
// ---------------------------------------------------------------------------

func TestVerifyCompletionReportsItsSourcesHonestly(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	id, _ := createTask(t, stack, map[string]any{
		"title":           "Отправить отчёт клиенту",
		"expected_result": "Отчёт отправлен клиенту на согласование",
	})

	recorder, report := apiRequest(t, stack.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/verify", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("verify: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if report["verdict"] != "insufficient" {
		t.Fatalf("verdict = %v, want insufficient without evidence", report["verdict"])
	}
	sources, _ := report["sources"].([]any)
	if len(sources) == 0 {
		t.Fatal("the report does not say which sources were consulted")
	}
	seenGitHub := false
	for _, entry := range sources {
		source, _ := entry.(map[string]any)
		if source["name"] == "github" {
			seenGitHub = true
			if source["available"] != false || source["reason"] == "" {
				t.Fatalf("github reported as %+v, want an explicit unavailability", source)
			}
		}
	}
	if !seenGitHub {
		t.Fatal("github was not reported at all")
	}

	// The task stays open: a check never decides on its own.
	_, task := apiRequest(t, stack.handler, "GET", "/api/v1/tasks/"+itoa(id), nil, nil)
	if task["status"] != "open" {
		t.Fatalf("status = %v after a check, want open", task["status"])
	}

	// The grounds are kept and can be read back.
	recorder, stored := apiRequest(t, stack.handler, "GET", "/api/v1/tasks/"+itoa(id)+"/verification", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("stored check: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if stored["verdict"] != "insufficient" || stored["reason"] == "" {
		t.Fatalf("stored check = %+v, want the recorded verdict and reason", stored)
	}

	// Declining keeps the task open and is remembered.
	recorder, decision := apiRequest(t, stack.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/verify/decision",
		map[string]any{"decision": "declined"}, nil)
	if recorder.Code != 200 {
		t.Fatalf("decline: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if decision["decision"] != "declined" {
		t.Fatalf("decision = %v, want declined", decision["decision"])
	}
}
