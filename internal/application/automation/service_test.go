package automation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// stepClock is a controllable clock.
type stepClock struct{ at time.Time }

func (c *stepClock) Now() time.Time { return c.at }

func (c *stepClock) add(d time.Duration) { c.at = c.at.Add(d) }

func testClock() *stepClock {
	return &stepClock{at: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)}
}

// recordedPublisher remembers what the service announced live.
type recordedPublisher struct {
	events.Broker
	published []string
}

func (p *recordedPublisher) Publish(eventType string, _ any) error {
	p.published = append(p.published, eventType)
	return nil
}

// fakeTools records executed steps and answers with canned results.
type fakeTools struct {
	results map[string]any
	calls   []string
	failFor map[string]bool
}

func (t *fakeTools) Execute(_ context.Context, name string, _ json.RawMessage) (any, error) {
	t.calls = append(t.calls, name)
	if t.failFor[name] {
		return nil, errors.New("шаг сломался")
	}
	if result, ok := t.results[name]; ok {
		return result, nil
	}
	return map[string]any{"ok": true}, nil
}

type fakeValidator struct{ known map[string]bool }

func (v fakeValidator) AllowedForAutomation(name string) (bool, string) {
	if v.known[name] {
		return true, ""
	}
	return false, "инструмент не зарегистрирован"
}

type fakeConditions struct{ keys map[string]struct{} }

func (c fakeConditions) CurrentInsightKeys(context.Context) (map[string]struct{}, error) {
	return c.keys, nil
}

// fakeRepo mirrors the SQLite semantics: versioned writes, a single running
// run reservation and a durable run journal.
type fakeRepo struct {
	mu          sync.Mutex
	automations map[int64]*domainautomation.Automation
	runs        map[int64]*domainautomation.Run
	nextID      int64
	// skipDelay slows RecordSkipped down, modelling a real database write.
	skipDelay time.Duration
	// skipGate, when set, holds a skip write until it is closed — a test
	// uses it to freeze the journal mid-drain.
	skipGate chan struct{}
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		automations: map[int64]*domainautomation.Automation{},
		runs:        map[int64]*domainautomation.Run{},
	}
}

// next allocates an id; callers hold r.mu.
func (r *fakeRepo) next() int64 {
	r.nextID++
	return r.nextID
}

func (r *fakeRepo) Insert(_ context.Context, automation *domainautomation.Automation) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := automation.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *automation
	stored.ID = id
	r.automations[id] = &stored
	return id, nil
}

func (r *fakeRepo) Get(_ context.Context, id int64) (*domainautomation.Automation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.automations[id]
	if !ok {
		return nil, appautomation.ErrNotFound
	}
	return stored, nil
}

func (r *fakeRepo) List(_ context.Context, filter appautomation.Filter) ([]*domainautomation.Automation, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]*domainautomation.Automation, 0)
	for _, item := range r.automations {
		if filter.Status != nil && item.Status != *filter.Status {
			continue
		}
		if filter.Query != "" && !strings.Contains(item.Title, filter.Query) {
			continue
		}
		found = append(found, item)
	}
	return found, len(found), nil
}

func (r *fakeRepo) Update(_ context.Context, id int64, expectedVersion int, automation *domainautomation.Automation) (*domainautomation.Automation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := automation.Validate(); err != nil {
		return nil, err
	}
	stored, ok := r.automations[id]
	if !ok {
		return nil, appautomation.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appautomation.ErrVersionConflict
	}
	storedCopy := *automation
	storedCopy.ID = id
	storedCopy.Version = stored.Version + 1
	r.automations[id] = &storedCopy
	return r.automations[id], nil
}

func (r *fakeRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.automations[id]; !ok {
		return appautomation.ErrNotFound
	}
	delete(r.automations, id)
	for runID, run := range r.runs {
		if run.AutomationID == id {
			delete(r.runs, runID)
		}
	}
	return nil
}

func (r *fakeRepo) ScheduleDue(_ context.Context, now time.Time) ([]*domainautomation.Automation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]*domainautomation.Automation, 0)
	for _, item := range r.automations {
		if item.Status != domainautomation.StatusActive || item.Trigger.Kind != domainautomation.TriggerSchedule {
			continue
		}
		if item.NextRunAt == nil || !item.NextRunAt.After(now) {
			found = append(found, item)
		}
	}
	return found, nil
}

func (r *fakeRepo) ListByTrigger(_ context.Context, kind domainautomation.TriggerKind) ([]*domainautomation.Automation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]*domainautomation.Automation, 0)
	for _, item := range r.automations {
		if item.Status == domainautomation.StatusActive && item.Trigger.Kind == kind {
			found = append(found, item)
		}
	}
	return found, nil
}

func (r *fakeRepo) StartRun(_ context.Context, automationID int64, trigger string, startedAt time.Time) (*domainautomation.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if run.AutomationID == automationID && run.Status == domainautomation.RunRunning {
			return nil, nil
		}
	}
	run := &domainautomation.Run{
		ID: r.next(), AutomationID: automationID, Trigger: trigger,
		Status: domainautomation.RunRunning, StartedAt: startedAt,
	}
	// The store keeps its own copy: the service mutates the returned value
	// while building the journal, and FinishRun must observe the stored
	// state, not the caller's working copy.
	r.runs[run.ID] = &domainautomation.Run{
		ID: run.ID, AutomationID: run.AutomationID, Trigger: run.Trigger,
		Status: run.Status, StartedAt: run.StartedAt,
	}
	return run, nil
}

func (r *fakeRepo) FinishRun(_ context.Context, run *domainautomation.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.runs[run.ID]
	if !ok || stored.Status != domainautomation.RunRunning {
		return appautomation.ErrRunNotFound
	}
	stored.Status = run.Status
	stored.Steps = run.Steps
	stored.Summary = run.Summary
	stored.Error = run.Error
	stored.FinishedAt = run.FinishedAt
	return nil
}

func (r *fakeRepo) GetRun(_ context.Context, automationID, runID int64) (*domainautomation.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok || run.AutomationID != automationID {
		return nil, appautomation.ErrRunNotFound
	}
	return run, nil
}

func (r *fakeRepo) ListRuns(_ context.Context, automationID int64, limit, offset int) ([]*domainautomation.Run, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]*domainautomation.Run, 0)
	for _, run := range r.runs {
		if run.AutomationID == automationID {
			found = append(found, run)
		}
	}
	// Newest first, matching the SQL ordering: random map iteration would
	// make journal assertions flaky.
	sort.Slice(found, func(i, j int) bool { return found[i].ID > found[j].ID })
	total := len(found)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return found[offset:end], total, nil
}

func (r *fakeRepo) FailRun(_ context.Context, runID int64, reason string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.runs[runID]
	if !ok || stored.Status != domainautomation.RunRunning {
		return appautomation.ErrRunNotFound
	}
	stored.Status = domainautomation.RunFailed
	stored.Error = reason
	stored.FinishedAt = &at
	return nil
}

func (r *fakeRepo) RecoverRunning(_ context.Context, at time.Time) ([]*domainautomation.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recovered := []*domainautomation.Run{}
	for _, run := range r.runs {
		if run.Status != domainautomation.RunRunning {
			continue
		}
		run.Status = domainautomation.RunFailed
		run.Error = "выполнение прервано: LifeOS был перезапущен во время работы сценария"
		run.FinishedAt = &at
		recovered = append(recovered, run)
	}
	return recovered, nil
}

func (r *fakeRepo) RecordSkipped(_ context.Context, automationID int64, trigger, reason string, at time.Time) (int64, error) {
	// The gate is waited on before the lock, so a frozen write does not
	// block the whole store.
	if r.skipGate != nil {
		<-r.skipGate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skipDelay > 0 {
		time.Sleep(r.skipDelay)
	}
	id := r.next()
	r.runs[id] = &domainautomation.Run{
		ID: id, AutomationID: automationID, Trigger: trigger,
		Status: domainautomation.RunSkipped, Summary: "Запуск не выполнен",
		Error: reason, StartedAt: at, FinishedAt: &at,
	}
	return id, nil
}

type fakeUoW struct{ repo *fakeRepo }

// Do mirrors database/sql: a cancelled context fails the transaction before
// any statement runs, which is exactly the failure mode the journal writes
// must survive.
func (u fakeUoW) Do(ctx context.Context, fn func(context.Context, appautomation.Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx, u.repo)
}

// snapshotRuns returns a locked copy of every journal entry: tests read the
// journal from their own goroutine while run and journal goroutines write,
// so raw map access would be a data race.
func (r *fakeRepo) snapshotRuns() []*domainautomation.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domainautomation.Run, 0, len(r.runs))
	for _, run := range r.runs {
		copied := *run
		out = append(out, &copied)
	}
	return out
}

func (r *fakeRepo) Automations() appautomation.Repository { return r }

func scheduleTrigger(hour, minute int) domainautomation.Trigger {
	return domainautomation.Trigger{
		Kind: domainautomation.TriggerSchedule,
		Schedule: &domainautomation.Schedule{
			Frequency: domainautomation.FrequencyDaily,
			Time:      fmt.Sprintf("%02d:%02d", hour, minute),
		},
	}
}

func sampleSteps() []domainautomation.Step {
	return []domainautomation.Step{
		{Name: "list_tasks", Arguments: json.RawMessage(`{"status":"open"}`), Description: "Открытые задачи"},
		{Name: "capture_inbox", Arguments: json.RawMessage(`{"text":"Проверка"}`), Description: "Напоминание во входящие"},
	}
}

func newTestService(clock *stepClock, repo *fakeRepo, tools appautomation.ToolExecutor, publisher events.Publisher) *appautomation.Service {
	return appautomation.NewService(
		fakeUoW{repo: repo},
		clock,
		appautomation.WithPublisher(publisher),
		appautomation.WithTools(tools),
		appautomation.WithValidator(fakeValidator{known: map[string]bool{"list_tasks": true, "capture_inbox": true}}),
		appautomation.WithConditions(fakeConditions{keys: map[string]struct{}{"overdue": {}}}),
		appautomation.WithTimezone("UTC"),
	)
}

func mustCreate(t *testing.T, service *appautomation.Service, trigger domainautomation.Trigger) *appautomation.View {
	t.Helper()
	view, err := service.Create(context.Background(), appautomation.CreateInput{
		Title: "Проверка", Trigger: trigger, Steps: sampleSteps(),
		ExpectedResult: "Напоминание во входящих",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return view
}

func TestCreateStartsProposedAndWaitsForApproval(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})

	view := mustCreate(t, service, scheduleTrigger(10, 0))
	if view.Automation.Status != domainautomation.StatusProposed {
		t.Fatalf("status = %s, want proposed", view.Automation.Status)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("designing must not execute steps, got %v", tools.calls)
	}

	// An approved automation gets a schedule bookmark computed from now.
	active, err := service.Approve(context.Background(), view.Automation.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if active.Automation.Status != domainautomation.StatusActive {
		t.Fatalf("status = %s, want active", active.Automation.Status)
	}
	if active.Automation.NextRunAt == nil {
		t.Fatalf("approval must compute the next schedule moment")
	}
	if !active.Automation.NextRunAt.After(clock.Now()) {
		t.Fatalf("next run %v must be after now %v", active.Automation.NextRunAt, clock.Now())
	}
}

func TestRefusesScenarioWithUnknownTool(t *testing.T) {
	service := newTestService(testClock(), newFakeRepo(), &fakeTools{}, &recordedPublisher{})
	_, err := service.Create(context.Background(), appautomation.CreateInput{
		Title: "Сломанный сценарий",
		Trigger: domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "task.completed"},
		},
		Steps: []domainautomation.Step{{Name: "not_registered"}},
	})
	if !errors.Is(err, appautomation.ErrStepNotAllowed) {
		t.Fatalf("err = %v, want ErrStepNotAllowed", err)
	}
}

func TestTickRunsDueScheduleAndJournalsSteps(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{results: map[string]any{"list_tasks": map[string]any{"total": 2}}}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, scheduleTrigger(9, 30))
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Not due yet: 10:00 pass, the moment is 9:30.
	result := service.Tick(context.Background(), clock.Now())
	if result.ScheduleRuns != 0 {
		t.Fatalf("runs = %d, want 0 before the moment", result.ScheduleRuns)
	}

	clock.at = time.Date(2026, 11, 16, 9, 30, 5, 0, time.UTC)
	result = service.Tick(context.Background(), clock.Now())
	if result.ScheduleRuns != 1 {
		t.Fatalf("runs = %d, want 1", result.ScheduleRuns)
	}
	if len(tools.calls) != len(sampleSteps()) {
		t.Fatalf("executed %v, want every step once", tools.calls)
	}
	runs, total, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("runs: %d, total %d, err %v", len(runs), total, err)
	}
	if runs[0].Status != domainautomation.RunSucceeded {
		t.Fatalf("run status = %s", runs[0].Status)
	}
	for i, step := range runs[0].Steps {
		if step.Status != domainautomation.StepSucceeded {
			t.Fatalf("step %d status = %s", i, step.Status)
		}
	}

	// The bookmark moved to tomorrow, so the next pass does not re-run.
	next := service.Tick(context.Background(), clock.Now())
	if next.ScheduleRuns != 0 {
		t.Fatalf("double run on the same pass cycle, got %d", next.ScheduleRuns)
	}

	// A missed moment is caught up exactly once after a restart gap.
	clock.at = time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	result = service.Tick(context.Background(), clock.Now())
	if result.ScheduleRuns != 1 || result.OverdueCatchups != 1 {
		t.Fatalf("catch-up: runs %d, catchups %d", result.ScheduleRuns, result.OverdueCatchups)
	}
}

func TestFailingStepStopsScenarioAndRecordsSkip(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{failFor: map[string]bool{"list_tasks": true}}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.completed"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	service.Observe(context.Background(), "task.completed", map[string]any{"id": 5}, clock.Now())
	runs, _, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if runs[0].Status != domainautomation.RunFailed {
		t.Fatalf("status = %s, want failed", runs[0].Status)
	}
	if runs[0].Steps[0].Status != domainautomation.StepFailed || runs[0].Steps[0].Error == "" {
		t.Fatalf("failing step must carry the error, got %+v", runs[0].Steps[0])
	}
	if runs[0].Steps[1].Status != domainautomation.StepSkipped {
		t.Fatalf("step after failure must be skipped, got %s", runs[0].Steps[1].Status)
	}
	if !strings.Contains(runs[0].Error, "шаг 1") {
		t.Fatalf("run error must name the failing step, got %q", runs[0].Error)
	}
}

func TestEventTriggerMatchesPayload(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind: domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{
			Name:  "task.completed",
			Match: map[string]string{"source": "assistant"},
		},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Wrong payload field: no run.
	service.Observe(context.Background(), "task.completed", map[string]any{"source": "user"}, clock.Now())
	runs, _, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("matched a payload it must not: %d runs", len(runs))
	}

	// Nested payload paths match by string form.
	service.Observe(context.Background(), "task.completed", map[string]any{
		"task": map[string]any{"id": 5},
	}, clock.Now())
	runs, _, _ = service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if len(runs) != 0 {
		t.Fatalf("a non-matching payload ran the scenario: %d", len(runs))
	}

	service.Observe(context.Background(), "task.completed", map[string]any{"source": "assistant"}, clock.Now())
	runs, _, _ = service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if len(runs) != 1 || runs[0].Trigger != "event" {
		t.Fatalf("matching payload must run once, got %+v", runs)
	}
}

func TestConditionRunsOnInsightWithCooldown(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind: domainautomation.TriggerCondition,
		Condition: &domainautomation.ConditionTrigger{
			Insight: "overdue", CooldownMinutes: 60,
		},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	result := service.Tick(context.Background(), clock.Now())
	if result.ConditionRuns != 1 {
		t.Fatalf("runs = %d, want 1 while the condition holds", result.ConditionRuns)
	}

	// The cooldown suppresses an immediate refire.
	result = service.Tick(context.Background(), clock.Now())
	if result.ConditionRuns != 0 {
		t.Fatalf("cooldown did not hold, got %d runs", result.ConditionRuns)
	}

	clock.at = clock.Now().Add(2 * time.Hour)
	result = service.Tick(context.Background(), clock.Now())
	if result.ConditionRuns != 1 {
		t.Fatalf("runs after cooldown = %d, want 1", result.ConditionRuns)
	}

	// A paused automation never runs, whatever the insight says.
	if _, err := service.SetStatus(context.Background(), view.Automation.ID, domainautomation.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	clock.at = clock.Now().Add(2 * time.Hour)
	result = service.Tick(context.Background(), clock.Now())
	if result.ConditionRuns != 0 {
		t.Fatalf("paused automation ran: %d", result.ConditionRuns)
	}
}

func TestActiveScenarioChangesRequirePause(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, scheduleTrigger(10, 0))
	active, err := service.Approve(context.Background(), view.Automation.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	title := "Переименовал"
	if _, err := service.Update(context.Background(), view.Automation.ID, active.Automation.Version, appautomation.UpdateInput{Title: &title}); err != nil {
		t.Fatalf("text edit on active automation: %v", err)
	}
	trigger := scheduleTrigger(11, 0)
	current, err := service.Get(context.Background(), view.Automation.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := service.Update(context.Background(), view.Automation.ID, current.Automation.Version, appautomation.UpdateInput{Trigger: &trigger}); !errors.Is(err, appautomation.ErrInvalidTransition) {
		t.Fatalf("trigger edit on active automation: %v", err)
	}

	if _, err := service.SetStatus(context.Background(), view.Automation.ID, domainautomation.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	current, err = service.Get(context.Background(), view.Automation.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := service.Update(context.Background(), view.Automation.ID, current.Automation.Version, appautomation.UpdateInput{Trigger: &trigger}); err != nil {
		t.Fatalf("trigger edit while paused: %v", err)
	}
}

func TestManualRunRequiresApproval(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, scheduleTrigger(10, 0))
	if _, err := service.RunNow(context.Background(), view.Automation.ID); !errors.Is(err, appautomation.ErrNotExecutable) {
		t.Fatalf("proposed automation must refuse a manual run, got %v", err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("nothing must execute, got %v", tools.calls)
	}
}

func TestNextRunScheduleArithmetic(t *testing.T) {
	loc := time.UTC
	daily := scheduleTrigger(9, 30)
	next := domainautomation.NextRun(daily, time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), loc)
	if next == nil || next.Format("2006-01-02 15:04") != "2026-11-17 09:30" {
		t.Fatalf("daily next = %v", next)
	}

	weekly := domainautomation.Trigger{
		Kind: domainautomation.TriggerSchedule,
		Schedule: &domainautomation.Schedule{
			Frequency: domainautomation.FrequencyWeekly, Time: "08:00",
			Weekdays: []int{1, 3}, // Monday, Wednesday
		},
	}
	next = domainautomation.NextRun(weekly, time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), loc) // Monday after 08:00
	if next == nil || next.Format("2006-01-02 15:04") != "2026-11-18 08:00" {
		t.Fatalf("weekly next = %v", next)
	}

	monthly := domainautomation.Trigger{
		Kind: domainautomation.TriggerSchedule,
		Schedule: &domainautomation.Schedule{
			Frequency: domainautomation.FrequencyMonthly, Time: "12:00", DayOfMonth: 31,
		},
	}
	next = domainautomation.NextRun(monthly, time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), loc)
	if next == nil || next.Format("2006-01-02 15:04") != "2026-12-31 12:00" {
		t.Fatalf("monthly next = %v (November has no 31st)", next)
	}

	monthlyLast := domainautomation.Trigger{
		Kind: domainautomation.TriggerSchedule,
		Schedule: &domainautomation.Schedule{
			Frequency: domainautomation.FrequencyMonthly, Time: "12:00", DayOfMonth: 0,
		},
	}
	next = domainautomation.NextRun(monthlyLast, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), loc)
	if next == nil || next.Format("2006-01-02 15:04") != "2026-02-28 12:00" {
		t.Fatalf("last-day monthly next = %v", next)
	}

	if domainautomation.NextRun(domainautomation.Trigger{Kind: domainautomation.TriggerEvent}, time.Now(), loc) != nil {
		t.Fatalf("event trigger has no schedule")
	}
}

func TestChainDepthRefusesSelfSustainedEvents(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.created"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// A direct human action is depth zero and runs.
	if err := service.RunFromEvent(context.Background(), view.Automation.ID, 0, clock.Now()); err != nil {
		t.Fatalf("depth 0 run: %v", err)
	}
	runs, total, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("runs after depth-0 event: %d, total %d, err %v", len(runs), total, err)
	}

	// An event published while the automation itself was running is depth
	// maxChainDepth after enough hops: it must not run and must leave a
	// visible skipped entry with the reason instead.
	err = service.RunFromEvent(context.Background(), view.Automation.ID, maxChainDepthTest, clock.Now())
	if err != nil {
		t.Fatalf("depth-cap run: %v", err)
	}
	runs, total, err = service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if total != 2 {
		t.Fatalf("total runs = %d, want 1 real + 1 skipped", total)
	}
	skipped := runs[0]
	if skipped.Status != domainautomation.RunSkipped || skipped.Error == "" {
		t.Fatalf("refused run must be a skipped journal entry with a reason: %+v", skipped)
	}
	if len(tools.calls) != len(sampleSteps()) {
		t.Fatalf("refused run executed steps: %v", tools.calls)
	}
}

// maxChainDepthTest mirrors the service's own limit; the test asserts the
// refused depth rather than an arbitrary number.
const maxChainDepthTest = 3

func TestRecoverStaleRunsReleasesReservation(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	tools := &fakeTools{}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, scheduleTrigger(9, 30))
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Simulate a crash mid-run: a running reservation without a finish.
	if _, err := repo.StartRun(context.Background(), view.Automation.ID, "schedule", clock.Now()); err != nil {
		t.Fatalf("simulate crash: %v", err)
	}

	// While the stale reservation holds, the due moment cannot run again.
	clock.at = time.Date(2026, 11, 16, 9, 31, 0, 0, time.UTC)
	result := service.Tick(context.Background(), clock.Now())
	if result.ScheduleRuns != 0 {
		t.Fatalf("a stale reservation must block re-running, got %d", result.ScheduleRuns)
	}

	// Startup recovery releases it as a failed run with the reason.
	recovered, err := service.RecoverStaleRuns(context.Background())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("recovered = %d runs, want 1", len(recovered))
	}
	runs, _, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if runs[0].Status != domainautomation.RunFailed || runs[0].Error == "" {
		t.Fatalf("recovered run must be failed with a reason: %+v", runs[0])
	}

	// The next pass runs the due schedule again.
	clock.at = time.Date(2026, 11, 16, 9, 40, 0, 0, time.UTC)
	result = service.Tick(context.Background(), clock.Now())
	if result.ScheduleRuns != 1 {
		t.Fatalf("schedule must run again after recovery, got %d", result.ScheduleRuns)
	}
}

// blockingTools holds the first step until the test releases it, which is
// what keeps a run reservation open across two observes.
type blockingTools struct {
	release chan struct{}
	calls   []string
	mu      sync.Mutex
}

func (t *blockingTools) Execute(_ context.Context, name string, _ json.RawMessage) (any, error) {
	t.mu.Lock()
	t.calls = append(t.calls, name)
	t.mu.Unlock()
	if name == "list_tasks" {
		<-t.release
	}
	return map[string]any{"ok": true}, nil
}

func TestEventTriggerOverlappingRunRecordedAsSkipped(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.completed"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Observe(context.Background(), "task.completed", map[string]any{}, clock.Now())
	}()
	// The first run holds the reservation on its first step.
	time.Sleep(50 * time.Millisecond)

	// A second event while the first run holds the reservation is refused
	// with a visible journal entry, not a silent log line.
	service.Observe(context.Background(), "task.completed", map[string]any{}, clock.Now())
	close(release)
	<-done

	runs, total, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if total != 2 {
		t.Fatalf("total runs = %d, want executed + skipped", total)
	}
	if runs[0].Status != domainautomation.RunSkipped || runs[0].Error == "" {
		t.Fatalf("overlapping run must be skipped with a reason: %+v", runs[0])
	}
	tools.mu.Lock()
	count := len(tools.calls)
	tools.mu.Unlock()
	if count != len(sampleSteps()) {
		t.Fatalf("the second event must not execute steps: %d calls", count)
	}
}

// TestJournalSurvivesCancelledContext pins the P1 from the reliability
// review: the user closing the HTTP connection cancels the request context,
// and the journal write must still land — the reservation must not survive
// the connection.
func TestJournalSurvivesCancelledContext(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	service := newTestService(clock, repo, &fakeTools{}, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.completed"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Observe(ctx, "task.completed", map[string]any{}, clock.Now())
	}()
	// Cancel while the run is executing: the finish transaction and the
	// fallback must run on a context that ignores this cancellation.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	runs, _, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if !runs[0].Status.Terminal() {
		t.Fatalf("run must reach a terminal status despite the cancelled context: %+v", runs[0])
	}
	// The reservation is released: the next event can run again.
	service.Observe(context.Background(), "task.completed", map[string]any{}, clock.Now())
	runs, total, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs after cancel: %v", err)
	}
	if total != 2 {
		t.Fatalf("total runs = %d, want 2: the released reservation must not block", total)
	}
}

// TestEventTriggerRejectsRunLifecycleEvents pins the structural cycle guard
// at the creation boundary: automation.run.* events are published by every
// run — including a depth-refused skip — so they must never be triggerable.
func TestEventTriggerRejectsRunLifecycleEvents(t *testing.T) {
	clock := testClock()
	service := newTestService(clock, newFakeRepo(), &fakeTools{}, &recordedPublisher{})
	for _, name := range []string{"automation.run.started", "automation.run.finished"} {
		_, err := service.Create(context.Background(), appautomation.CreateInput{
			Title: "Проверка",
			Trigger: domainautomation.Trigger{
				Kind:  domainautomation.TriggerEvent,
				Event: &domainautomation.EventTrigger{Name: name},
			},
			Steps:          sampleSteps(),
			ExpectedResult: "Напоминание во входящих",
		})
		if err == nil {
			t.Fatalf("trigger on %q must be refused", name)
		}
	}
}
