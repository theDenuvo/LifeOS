package httptransport

import (
	"testing"
	"time"
)

// createTask posts a task and returns its id and version.
func createTask(t *testing.T, stack *planningStack, body map[string]any) (int64, int64) {
	t.Helper()
	recorder, created := apiRequest(t, stack.handler, "POST", "/api/v1/tasks", body, nil)
	if recorder.Code != 201 {
		t.Fatalf("create task %v: status %d, body %s", body["title"], recorder.Code, recorder.Body.String())
	}
	return int64(created["id"].(float64)), int64(created["version"].(float64))
}

// createEvent posts a calendar event.
func createEvent(t *testing.T, stack *planningStack, title string, start, end time.Time) {
	t.Helper()
	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/calendar/events", map[string]any{
		"title":     title,
		"calendar":  "work",
		"starts_at": start.Format(time.RFC3339),
		"ends_at":   end.Format(time.RFC3339),
	}, nil)
	if recorder.Code != 201 {
		t.Fatalf("create event %q: status %d, body %s", title, recorder.Code, recorder.Body.String())
	}
}

// proposedItems converts the plan proposal into apply items.
func proposedItems(t *testing.T, plan map[string]any) []map[string]any {
	t.Helper()
	proposed, _ := plan["proposed"].([]any)
	items := make([]map[string]any, 0, len(proposed))
	for _, entry := range proposed {
		block, _ := entry.(map[string]any)
		items = append(items, map[string]any{
			"task_id": block["task_id"],
			"start":   block["start"],
			"end":     block["end"],
		})
	}
	return items
}

// dayPlan fetches the plan of a date.
func dayPlan(t *testing.T, stack *planningStack, date string) map[string]any {
	t.Helper()
	recorder, plan := apiRequest(t, stack.handler, "GET", "/api/v1/plan/day?date="+date, nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("plan day %s: status %d, body %s", date, recorder.Code, recorder.Body.String())
	}
	return plan
}

// ---------------------------------------------------------------------------
// P-01: planning in the past
// ---------------------------------------------------------------------------

func TestPlanDayNeverProposesTimeThatAlreadyPassed(t *testing.T) {
	now := moscow(t, "2026-11-09 15:00") // Monday afternoon
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	createTask(t, stack, map[string]any{"title": "Написать отчёт", "estimate_minutes": 60})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want one block for the remaining hour", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	start, err := time.Parse(time.RFC3339, block["start"].(string))
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	if start.Before(now) {
		t.Fatalf("proposal starts at %s, before the current moment %s", start.In(now.Location()), now)
	}
	if plan["past"] != false {
		t.Errorf("past = %v, want false for today", plan["past"])
	}
}

func TestApplyPlanRejectsTimeThatAlreadyPassed(t *testing.T) {
	now := moscow(t, "2026-11-09 15:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	taskID, _ := createTask(t, stack, map[string]any{"title": "Утренняя задача", "estimate_minutes": 30})

	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-09 09:00").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-09 09:30").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 400 {
		t.Fatalf("apply in the past: status %d, want 400; body %s", recorder.Code, recorder.Body.String())
	}
	_, plan := apiRequest(t, stack.handler, "GET", "/api/v1/plan/day?date=2026-11-09", nil, nil)
	if blocks, _ := plan["blocks"].([]any); len(blocks) != 0 {
		t.Fatalf("a rejected plan wrote %d blocks", len(blocks))
	}
}

func TestPlanDayForFutureDayUsesTheWholeWorkday(t *testing.T) {
	now := moscow(t, "2026-11-09 15:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	createTask(t, stack, map[string]any{"title": "Завтрашняя работа", "estimate_minutes": 120})

	plan := dayPlan(t, stack, "2026-11-10")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want one block tomorrow", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	start, _ := time.Parse(time.RFC3339, block["start"].(string))
	if !start.In(now.Location()).Equal(moscow(t, "2026-11-10 09:10")) {
		t.Fatalf("start = %s, want 09:10 after the buffer", start.In(now.Location()))
	}
}

func TestPlanDayForFinishedDayProposesNothing(t *testing.T) {
	now := moscow(t, "2026-11-09 15:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	createTask(t, stack, map[string]any{"title": "Старая работа", "estimate_minutes": 60})

	plan := dayPlan(t, stack, "2026-11-06") // previous Friday
	if plan["past"] != true {
		t.Fatalf("past = %v, want true for a finished day", plan["past"])
	}
	if proposed, _ := plan["proposed"].([]any); len(proposed) != 0 {
		t.Fatalf("a finished day still proposes %d blocks", len(proposed))
	}
	if free, _ := plan["free_minutes"].(float64); free != 0 {
		t.Fatalf("free_minutes = %v on a finished day, want 0", free)
	}
}

// ---------------------------------------------------------------------------
// P-02: existing automatic bookings must survive a partial acceptance
// ---------------------------------------------------------------------------

func TestAcceptingANewTaskKeepsTheExistingPlan(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00") // Monday
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	firstID, _ := createTask(t, stack, map[string]any{"title": "Первая", "estimate_minutes": 60})
	secondID, _ := createTask(t, stack, map[string]any{"title": "Вторая", "estimate_minutes": 60})

	plan := dayPlan(t, stack, "2026-11-09")
	items := proposedItems(t, plan)
	if len(items) != 2 {
		t.Fatalf("proposed %d blocks, want 2", len(items))
	}
	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items":         items,
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("apply: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	// A third task appears during the day and is planned on top of the existing
	// plan. Accepting it must not remove the two blocks that were already there.
	thirdID, _ := createTask(t, stack, map[string]any{"title": "Третья", "estimate_minutes": 30})
	plan = dayPlan(t, stack, "2026-11-09")
	if blocks, _ := plan["blocks"].([]any); len(blocks) != 2 {
		t.Fatalf("existing blocks = %d, want 2", len(blocks))
	}
	items = proposedItems(t, plan)
	if len(items) != 1 {
		t.Fatalf("second proposal = %+v, want only the new task", plan["proposed"])
	}
	recorder, _ = apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items":         items,
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("apply new task: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	plan = dayPlan(t, stack, "2026-11-09")
	blocks, _ := plan["blocks"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("blocks after accepting the third task = %d, want 3: %+v", len(blocks), plan["blocks"])
	}
	seen := map[float64]bool{}
	for _, entry := range blocks {
		block, _ := entry.(map[string]any)
		seen[block["task_id"].(float64)] = true
	}
	for _, id := range []int64{firstID, secondID, thirdID} {
		if !seen[float64(id)] {
			t.Errorf("task %d lost its reserved time", id)
		}
	}

	// Accepting the very same request again must be a no-op, not a duplicate.
	recorder, _ = apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items":         items,
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("re-apply: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	plan = dayPlan(t, stack, "2026-11-09")
	if blocks, _ := plan["blocks"].([]any); len(blocks) != 3 {
		t.Fatalf("re-applying duplicated blocks: %d", len(blocks))
	}
}

func TestFailedPlanApplicationKeepsTheOldSchedule(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	taskID, _ := createTask(t, stack, map[string]any{"title": "Работа", "estimate_minutes": 60})
	plan := dayPlan(t, stack, "2026-11-09")
	items := proposedItems(t, plan)
	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items":         items,
	}, nil)
	if recorder.Code != 200 {
		t.Fatalf("apply: status %d", recorder.Code)
	}

	createEvent(t, stack, "Встреча", moscow(t, "2026-11-09 12:00"), moscow(t, "2026-11-09 13:00"))

	// The new request is invalid (it overlaps the appointment), so the whole
	// transaction must roll back, including the block it would have replaced.
	recorder, _ = apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-09 12:15").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-09 12:45").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 409 {
		t.Fatalf("overlapping apply: status %d, want 409; body %s", recorder.Code, recorder.Body.String())
	}

	plan = dayPlan(t, stack, "2026-11-09")
	blocks, _ := plan["blocks"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("blocks after a rejected apply = %d, want the original one", len(blocks))
	}
	block, _ := blocks[0].(map[string]any)
	start, _ := time.Parse(time.RFC3339, block["start"].(string))
	if !start.In(now.Location()).Equal(moscow(t, "2026-11-09 09:10")) {
		t.Fatalf("the original block moved to %s", start.In(now.Location()))
	}
}

// ---------------------------------------------------------------------------
// P-03: exact deadlines
// ---------------------------------------------------------------------------

func TestPlanDayRespectsAnExactDeadline(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00") // Monday
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	// The whole morning is taken, so the first free moment is 11:40.
	createEvent(t, stack, "Созвон", moscow(t, "2026-11-09 09:00"), moscow(t, "2026-11-09 11:30"))

	createTask(t, stack, map[string]any{
		"title":            "Сдать до полудня",
		"estimate_minutes": 60,
		"due_at":           moscow(t, "2026-11-09 12:00").Format(time.RFC3339),
	})
	createTask(t, stack, map[string]any{
		"title":            "Сдать до трёх",
		"estimate_minutes": 60,
		"due_at":           moscow(t, "2026-11-09 15:00").Format(time.RFC3339),
	})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	unplaced, _ := plan["unplaced"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want one block (the one that fits)", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	if block["title"] != "Сдать до трёх" {
		t.Fatalf("planned %v, want the task whose deadline can still be met", block["title"])
	}
	if len(unplaced) != 1 {
		t.Fatalf("unplaced = %+v, want the task that cannot make its deadline", plan["unplaced"])
	}
	missing, _ := unplaced[0].(map[string]any)
	if missing["title"] != "Сдать до полудня" {
		t.Fatalf("unplaced %v, want «Сдать до полудня»", missing["title"])
	}
	reason, _ := missing["reason"].(string)
	if reason == "" || !contains(reason, "дедлайн") {
		t.Fatalf("reason = %q, want an explanation about the deadline", reason)
	}
}

func TestApplyPlanRejectsABlockThatMissesTheDeadline(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	taskID, _ := createTask(t, stack, map[string]any{
		"title":            "Отчёт",
		"estimate_minutes": 60,
		"due_at":           moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
	})

	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-09"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-09 09:10").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-09 10:10").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 400 {
		t.Fatalf("apply after the deadline: status %d, want 400; body %s", recorder.Code, recorder.Body.String())
	}
}

func TestApplyPlanRejectsADateOnlyDeadlineViolation(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	taskID, _ := createTask(t, stack, map[string]any{
		"title":            "Квартальный отчёт",
		"estimate_minutes": 60,
		"due_date":         "2026-11-09",
	})

	recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", map[string]any{
		"replace_dates": []string{"2026-11-10"},
		"items": []map[string]any{{
			"task_id": taskID,
			"start":   moscow(t, "2026-11-10 09:10").Format(time.RFC3339),
			"end":     moscow(t, "2026-11-10 10:10").Format(time.RFC3339),
		}},
	}, nil)
	if recorder.Code != 400 {
		t.Fatalf("apply after a calendar deadline: status %d, want 400; body %s", recorder.Code, recorder.Body.String())
	}
}

func TestOverdueExactDeadlineNoLongerBlocksPlanning(t *testing.T) {
	now := moscow(t, "2026-11-09 15:00") // the deadline is already in the past
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	createTask(t, stack, map[string]any{
		"title":            "Просроченная",
		"estimate_minutes": 30,
		"due_at":           moscow(t, "2026-11-09 10:00").Format(time.RFC3339),
	})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want the overdue task planned as early as possible", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	start, _ := time.Parse(time.RFC3339, block["start"].(string))
	if start.Before(now) {
		t.Fatalf("overdue work planned at %s, before now", start.In(now.Location()))
	}
}

// ---------------------------------------------------------------------------
// P-04: no second block for work that already has one
// ---------------------------------------------------------------------------

func TestReplanDoesNotScheduleTasksThatAlreadyHaveTime(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	manualID, _ := createTask(t, stack, map[string]any{"title": "Ручной блок", "estimate_minutes": 60})
	freeID, _ := createTask(t, stack, map[string]any{"title": "Без времени", "estimate_minutes": 60})

	// A block the user placed by hand is a commitment, not something to replace.
	if _, err := stack.db.Exec(`
		INSERT INTO task_blocks (task_id, starts_at, ends_at, status, source, created_at, updated_at)
		VALUES (?, ?, ?, 'planned', 'manual', ?, ?)`,
		manualID,
		moscow(t, "2026-11-09 09:00").Unix(),
		moscow(t, "2026-11-09 10:00").Unix(),
		now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert manual block: %v", err)
	}

	recorder, proposal := apiRequest(t, stack.handler, "GET", "/api/v1/plan/replan?days=3", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("replan: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	moves, _ := proposal["moves"].([]any)
	if len(moves) != 1 {
		t.Fatalf("moves = %+v, want only the task without time", proposal["moves"])
	}
	move, _ := moves[0].(map[string]any)
	if int64(move["task_id"].(float64)) != freeID {
		t.Fatalf("proposed a move for task %v, want %d", move["task_id"], freeID)
	}
	kept, _ := proposal["already_placed"].([]any)
	if len(kept) != 1 {
		t.Fatalf("already_placed = %+v, want the manually placed task", proposal["already_placed"])
	}
	entry, _ := kept[0].(map[string]any)
	if int64(entry["task_id"].(float64)) != manualID {
		t.Fatalf("already_placed task = %v, want %d", entry["task_id"], manualID)
	}

	// Applying the proposal twice must not add a second block either.
	placements, _ := proposal["placements"].([]any)
	placement, _ := placements[0].(map[string]any)
	items := []map[string]any{{
		"task_id": placement["task_id"],
		"start":   placement["start"],
		"end":     placement["end"],
	}}
	applyBody := map[string]any{
		"replace_dates": []string{placement["date"].(string)},
		"items":         items,
	}
	for i := 0; i < 2; i++ {
		recorder, _ = apiRequest(t, stack.handler, "POST", "/api/v1/plan/apply", applyBody, nil)
		if recorder.Code != 200 {
			t.Fatalf("apply replan proposal (pass %d): status %d, body %s", i+1, recorder.Code, recorder.Body.String())
		}
	}
	blocks := blockCount(t, stack)
	if blocks != 2 {
		t.Fatalf("blocks = %d, want one manual and one planned", blocks)
	}
}

// ---------------------------------------------------------------------------
// P-05: a day is only usable when it has one continuous window
// ---------------------------------------------------------------------------

func TestReplanPicksADayWithOneContinuousWindow(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00") // Monday
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	// Monday is fragmented into gaps far shorter than 90 minutes, while its total
	// free time is still large. Tuesday is completely free.
	createEvent(t, stack, "Блок 1", moscow(t, "2026-11-09 09:40"), moscow(t, "2026-11-09 11:00"))
	createEvent(t, stack, "Блок 2", moscow(t, "2026-11-09 11:40"), moscow(t, "2026-11-09 14:00"))
	createEvent(t, stack, "Блок 3", moscow(t, "2026-11-09 14:40"), moscow(t, "2026-11-09 19:00"))

	taskID, _ := createTask(t, stack, map[string]any{"title": "Большая работа", "estimate_minutes": 90})

	recorder, proposal := apiRequest(t, stack.handler, "GET", "/api/v1/plan/replan?days=2", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("replan: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if unplaced, _ := proposal["unplaced"].([]any); len(unplaced) != 0 {
		t.Fatalf("unplaced = %+v, but Tuesday has a 90-minute window", proposal["unplaced"])
	}
	moves, _ := proposal["moves"].([]any)
	if len(moves) != 1 {
		t.Fatalf("moves = %+v, want one", proposal["moves"])
	}
	move, _ := moves[0].(map[string]any)
	if int64(move["task_id"].(float64)) != taskID {
		t.Fatalf("move for task %v, want %d", move["task_id"], taskID)
	}
	if move["to"] != "2026-11-10" {
		t.Fatalf("moved to %v, want Tuesday the 10th", move["to"])
	}
}

// ---------------------------------------------------------------------------
// P-08: one rule for tasks without an estimate
// ---------------------------------------------------------------------------

func TestPlanUsesTheConfiguredDefaultEstimate(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	recorder, _ := apiRequest(t, stack.handler, "PUT", "/api/v1/settings/planning.default_estimate_minutes",
		map[string]any{"value": 45}, nil)
	if recorder.Code != 200 {
		t.Fatalf("set default estimate: status %d, body %s", recorder.Code, recorder.Body.String())
	}

	createTask(t, stack, map[string]any{"title": "Задача без оценки"})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want the task to be planned", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	if block["estimate_minutes"] != float64(45) {
		t.Errorf("estimate = %v, want the configured 45", block["estimate_minutes"])
	}
	if block["estimate_source"] != "default" {
		t.Errorf("estimate_source = %v, want default", block["estimate_source"])
	}

	// Replanning must apply exactly the same rule.
	recorder, proposal := apiRequest(t, stack.handler, "GET", "/api/v1/plan/replan?days=2", nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("replan: status %d", recorder.Code)
	}
	moves, _ := proposal["moves"].([]any)
	if len(moves) != 1 {
		t.Fatalf("replan skipped a task without an estimate: %+v", proposal)
	}
	move, _ := moves[0].(map[string]any)
	if move["estimate_minutes"] != float64(45) {
		t.Errorf("replan estimate = %v, want 45", move["estimate_minutes"])
	}
}

func TestPlanPrefersHistoryOverTheDefaultEstimate(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	for _, body := range []map[string]any{
		{"title": "Написать отчёт", "estimate_minutes": 100},
		{"title": "Написать отчёт по проекту", "estimate_minutes": 120},
	} {
		id, version := createTask(t, stack, body)
		recorder, _ := apiRequest(t, stack.handler, "POST", "/api/v1/tasks/"+itoa(id)+"/complete", nil,
			map[string]string{"If-Match": `"` + itoa(version) + `"`})
		if recorder.Code != 200 {
			t.Fatalf("complete: status %d, body %s", recorder.Code, recorder.Body.String())
		}
	}

	createTask(t, stack, map[string]any{"title": "Написать отчёт за неделю"})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v, want the new report", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	if block["estimate_source"] != "history" {
		t.Fatalf("estimate_source = %v, want history", block["estimate_source"])
	}
	if block["estimate_minutes"] != float64(120) {
		t.Errorf("estimate = %v, want the 120 median", block["estimate_minutes"])
	}
}

func TestExplicitEstimateWinsOverTheDefault(t *testing.T) {
	now := moscow(t, "2026-11-09 08:00")
	stack := newPlanningStack(t, now)
	defer stack.cleanup()

	recorder, _ := apiRequest(t, stack.handler, "PUT", "/api/v1/settings/planning.default_estimate_minutes",
		map[string]any{"value": 120}, nil)
	if recorder.Code != 200 {
		t.Fatalf("set default estimate: status %d", recorder.Code)
	}
	createTask(t, stack, map[string]any{"title": "Короткая задача", "estimate_minutes": 25})

	plan := dayPlan(t, stack, "2026-11-09")
	proposed, _ := plan["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("proposed = %+v", plan["proposed"])
	}
	block, _ := proposed[0].(map[string]any)
	if block["estimate_minutes"] != float64(25) || block["estimate_source"] != "explicit" {
		t.Fatalf("estimate = %v (%v), want the explicit 25", block["estimate_minutes"], block["estimate_source"])
	}
}

// blockCount returns how many work blocks exist.
func blockCount(t *testing.T, stack *planningStack) int {
	t.Helper()
	var count int
	if err := stack.db.QueryRow(`SELECT COUNT(1) FROM task_blocks`).Scan(&count); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	return count
}

// contains is a small helper so tests do not import strings for one check.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
