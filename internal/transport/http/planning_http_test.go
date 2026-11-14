package httptransport

import (
	"context"
	"testing"
	"time"

	appcalendar "github.com/theDenuvo/LifeOS/internal/application/calendar"
	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/application/recurrence"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/events"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

// newPlanningAPI builds a handler with the planner wired in, over a real
// temporary SQLite database: the planning rules, the repositories and the HTTP
// contract are all exercised together.
func newPlanningAPI(t *testing.T, now time.Time) (*Handler, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := sqlite.Open(tmpDir + "/planning.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := sqlite.Migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{DataDir: tmpDir, Timezone: "Europe/Moscow"}
	clock := apiClock{now: now}
	broker := events.NewBroker()

	var planner *planning.Service
	taskService := tasks.NewService(
		sqlite.NewTaskUnitOfWork(db),
		clock,
		tasks.WithDefaultTimezone("Europe/Moscow"),
		tasks.WithPublisher(broker),
		tasks.WithReminderSink(tasks.ReminderFunc(func(ctx context.Context, value task.Task) error {
			return planner.SyncTaskReminders(ctx, value)
		})),
	)
	planner = planning.NewService(
		sqlite.NewPlanningUnitOfWork(db),
		clock,
		planning.WithTimezone("Europe/Moscow"),
		planning.WithPublisher(broker),
	)
	recurrenceService := recurrence.NewService(taskService, clock, recurrence.WithTimezone("Europe/Moscow"))
	taskService.SetRecurrenceHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return recurrenceService.Next(ctx, completed)
	})

	inboxService := inbox.NewService(sqlite.NewInboxUnitOfWork(db), clock, "Europe/Moscow", inbox.WithPublisher(broker))
	eventService := appevents.NewService(
		sqlite.NewEventUnitOfWork(db),
		clock,
		appevents.WithTimezone("Europe/Moscow"),
		appevents.WithPublisher(broker),
	)
	calendarService := appcalendar.NewService(eventService, taskService, "Europe/Moscow")

	handler := NewHandler(db, cfg, testEmbedFS, Services{
		Tasks:    taskService,
		Inbox:    inboxService,
		Planning: planner,
		Events:   eventService,
		Calendar: calendarService,
		Broker:   broker,
	})
	handler.now = func() time.Time { return now }
	return handler, func() { _ = db.Close() }
}

// moscow builds an instant from a Moscow wall-clock time.
func moscow(t *testing.T, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func TestPlanDayProposesAndApplyReservesTime(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00") // Monday
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	// An appointment blocks the tenth hour of the working day.
	recorder, _ := apiRequest(t, handler, "POST", "/api/v1/calendar/events", map[string]any{
		"title":     "Созвон",
		"calendar":  "work",
		"starts_at": moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
		"ends_at":   moscow(t, "2026-11-09 11:00").Format(time.RFC3339),
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create event: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	for _, title := range []string{"Написать отчёт", "Разобрать почту"} {
		recorder, _ := apiRequest(t, handler, "POST", "/api/v1/tasks", map[string]any{
			"title":            title,
			"estimate_minutes": 60,
		}, nil)
		if recorder.Code != 201 {
			t.Fatalf("create task %q: status %d, body %s", title, recorder.Code, recorder.Body.String())
		}
	}

	recorder, plan := apiRequest(t, handler, "GET", "/api/v1/plan/day?date=2026-11-09", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("plan day: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if plan["working"] != true {
		t.Fatalf("Monday reported as a non-working day: %+v", plan)
	}
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 2 {
		t.Fatalf("proposed %d blocks, want 2: %+v", len(proposed), plan["proposed"])
	}

	items := make([]map[string]any, 0, len(proposed))
	for _, entry := range proposed {
		block, _ := entry.(map[string]any)
		items = append(items, map[string]any{
			"task_id": block["task_id"],
			"start":   block["start"],
			"end":     block["end"],
		})
	}

	recorder, applied := apiRequest(t, handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items":         items,
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("apply plan: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if count, _ := applied["applied"].([]any); len(count) != 2 {
		t.Fatalf("applied %d blocks, want 2: %+v", len(count), applied)
	}

	// The planner must not place work on top of the appointment.
	_, plan = apiRequest(t, handler, "GET", "/api/v1/plan/day?date=2026-11-09", nil, nil)
	blocks, _ := plan["blocks"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("stored blocks = %d, want 2: %+v", len(blocks), plan["blocks"])
	}
	appointmentStart := moscow(t, "2026-11-09 10:00")
	appointmentEnd := moscow(t, "2026-11-09 11:00")
	for _, entry := range blocks {
		block, _ := entry.(map[string]any)
		start, _ := time.Parse(time.RFC3339, block["start"].(string))
		end, _ := time.Parse(time.RFC3339, block["end"].(string))
		if start.Before(appointmentEnd) && appointmentStart.Before(end) {
			t.Errorf("block %s-%s overlaps the 10:00-11:00 appointment", start.In(appointmentStart.Location()), end.In(appointmentEnd.Location()))
		}
	}
}

func TestApplyPlanRejectsConflictsAndWeekends(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	recorder, task := apiRequest(t, handler, "POST", "/api/v1/tasks", map[string]any{
		"title":            "Позвонить",
		"estimate_minutes": 30,
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create task: %d", recorder.Code)
	}
	taskID := int64(task["id"].(float64))

	recorder, _ = apiRequest(t, handler, "POST", "/api/v1/calendar/events", map[string]any{
		"title":     "Встреча",
		"calendar":  "work",
		"starts_at": moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
		"ends_at":   moscow(t, "2026-11-09 11:00").Format(time.RFC3339),
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create event: %d", recorder.Code)
	}

	// Directly on top of the appointment.
	recorder, _ = apiRequest(t, handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-09 10:15").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-09 10:45").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 409 {
		t.Fatalf("overlapping plan: status %d, want 409; body %s", recorder.Code, recorder.Body.String())
	}

	// On a Saturday, outside the configured working days.
	recorder, _ = apiRequest(t, handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-14"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-14 10:00").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-14 10:30").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 400 {
		t.Fatalf("weekend plan: status %d, want 400; body %s", recorder.Code, recorder.Body.String())
	}
}

func TestPlanProposalSuggestsMoves(t *testing.T) {
	now := moscow(t, "2026-11-12 09:00") // Thursday
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	recorder, _ := apiRequest(t, handler, "POST", "/api/v1/tasks", map[string]any{
		"title":            "Просроченный отчёт",
		"priority":         "high",
		"estimate_minutes": 60,
		"due_date":         "2026-11-10",
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create overdue task: %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder, proposal := apiRequest(t, handler, "GET", "/api/v1/plan/replan?days=3", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("replan: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	moves, _ := proposal["moves"].([]any)
	if len(moves) != 1 {
		t.Fatalf("moves = %+v, want one move for the overdue task", proposal["moves"])
	}
	move, _ := moves[0].(map[string]any)
	if move["reason"] != "просрочено" {
		t.Errorf("reason = %v, want the overdue explanation", move["reason"])
	}
	placements, _ := proposal["placements"].([]any)
	if len(placements) != 1 {
		t.Fatalf("placements = %+v, want one", proposal["placements"])
	}

	// A proposal changes nothing by itself.
	_, day := apiRequest(t, handler, "GET", "/api/v1/plan/day?date=2026-11-12", nil, nil)
	if blocks, _ := day["blocks"].([]any); len(blocks) != 0 {
		t.Fatalf("replan wrote %d blocks; a proposal must not change data", len(blocks))
	}
}

func TestRemindersFollowTheDeadline(t *testing.T) {
	now := moscow(t, "2026-11-10 08:00")
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	recorder, task := apiRequest(t, handler, "POST", "/api/v1/tasks", map[string]any{
		"title":    "Сдать отчёт",
		"priority": "high",
		"due_at":   moscow(t, "2026-11-11 12:00").Format(time.RFC3339),
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create task: %d, body %s", recorder.Code, recorder.Body.String())
	}
	taskID := int64(task["id"].(float64))

	recorder, reminders := apiRequest(t, handler, "GET", "/api/v1/tasks/"+itoa(taskID)+"/reminders", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("reminders: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	data, _ := reminders["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("reminders = %+v, want three for a high-priority exact deadline", reminders["data"])
	}

	// Finishing the task clears what has not been delivered yet.
	version := int(task["version"].(float64))
	recorder, _ = apiRequest(t, handler, "POST", "/api/v1/tasks/"+itoa(taskID)+"/complete", nil,
		map[string]string{"If-Match": `"` + itoa(int64(version)) + `"`})
	if recorder.Code != 200 {
		t.Fatalf("complete: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	_, reminders = apiRequest(t, handler, "GET", "/api/v1/tasks/"+itoa(taskID)+"/reminders", nil, nil)
	if data, _ := reminders["data"].([]any); len(data) != 0 {
		t.Fatalf("completed task still has %d reminders", len(data))
	}
}

func TestCompletingRecurringTaskCreatesNextOccurrence(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	recorder, task := apiRequest(t, handler, "POST", "/api/v1/tasks", map[string]any{
		"title":            "Зарядка",
		"priority":         "medium",
		"planned_date":     "2026-11-09",
		"estimate_minutes": 20,
		"repeat":           map[string]any{"freq": "daily", "interval": 1, "window_days": 1},
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create recurring task: %d, body %s", recorder.Code, recorder.Body.String())
	}
	taskID := int64(task["id"].(float64))
	version := int64(task["version"].(float64))

	recorder, _ = apiRequest(t, handler, "POST", "/api/v1/tasks/"+itoa(taskID)+"/complete", nil,
		map[string]string{"If-Match": `"` + itoa(version) + `"`})
	if recorder.Code != 200 {
		t.Fatalf("complete: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	recorder, list := apiRequest(t, handler, "GET", "/api/v1/tasks?status=open&limit=10", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("list: status %d", recorder.Code)
	}
	items, _ := list["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("open tasks = %d, want the next occurrence: %+v", len(items), list["data"])
	}
	next, _ := items[0].(map[string]any)
	if next["planned_date"] != "2026-11-10" {
		t.Errorf("next occurrence planned for %v, want 2026-11-10", next["planned_date"])
	}
	if next["repeat"] == nil {
		t.Error("next occurrence lost the repeat rule")
	}
	if next["estimate_minutes"] != float64(20) {
		t.Errorf("next occurrence estimate = %v, want 20", next["estimate_minutes"])
	}
}

func TestInboxKeepsIdeasAsIdeas(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	handler, cleanup := newPlanningAPI(t, now)
	defer cleanup()

	recorder, idea := apiRequest(t, handler, "POST", "/api/v1/inbox", map[string]any{
		"text": "Идея: приложение для учёта книг",
		"kind": "idea",
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("capture idea: %d, body %s", recorder.Code, recorder.Body.String())
	}
	if idea["kind"] != "idea" {
		t.Fatalf("kind = %v, want idea", idea["kind"])
	}
	if idea["revisit_at"] == nil {
		t.Fatal("an idea without a date got no default revisit date")
	}

	// It is not a task and must not appear in the task list.
	_, tasksList := apiRequest(t, handler, "GET", "/api/v1/tasks?status=open", nil, nil)
	if data, _ := tasksList["data"].([]any); len(data) != 0 {
		t.Fatalf("an idea became a task: %+v", tasksList["data"])
	}

	recorder, ideas := apiRequest(t, handler, "GET", "/api/v1/inbox?kind=idea", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("list ideas: %d", recorder.Code)
	}
	if data, _ := ideas["data"].([]any); len(data) != 1 {
		t.Fatalf("ideas = %+v, want one", ideas["data"])
	}

	// It can be reclassified later, when the user decides it is work.
	id := int64(idea["id"].(float64))
	recorder, updated := apiRequest(t, handler, "PATCH", "/api/v1/inbox/"+itoa(id), map[string]any{
		"kind":       "task",
		"revisit_at": "2026-11-20",
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("reclassify: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if updated["kind"] != "task" || updated["revisit_at"] != "2026-11-20" {
		t.Fatalf("reclassified item = %+v", updated)
	}
}

// itoa renders an int64 without importing strconv in every test.
func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
