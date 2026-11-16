package proactive_test

// Roadmap stage 5: cross-referenced detectors and the initiative gate.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
	domainevent "github.com/theDenuvo/LifeOS/internal/domain/event"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
	task "github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

// mutableClock lets a test move time forward for cooldown windows.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *mutableClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// stage5Fixture owns one migrated database and the shared fakes.
type stage5Fixture struct {
	db        *sql.DB
	clock     *mutableClock
	tasks     *tasks.Service
	inbox     *fakeInbox
	uow       *sqlite.InsightUnitOfWork
	publisher *recordingOutreachPublisher
}

type fakeInbox struct {
	mu    sync.Mutex
	count int
}

func (f *fakeInbox) CountUnprocessed(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, nil
}

func newFixture(t *testing.T) *stage5Fixture {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/proactive-stage5.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 2026-11-12 12:00 UTC is 15:00 in Moscow — an ordinary working hour.
	clock := &mutableClock{now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	return &stage5Fixture{
		db:        db,
		clock:     clock,
		tasks:     tasks.NewService(sqlite.NewTaskUnitOfWork(db), clock, tasks.WithDefaultTimezone("Europe/Moscow")),
		inbox:     &fakeInbox{},
		uow:       sqlite.NewInsightUnitOfWork(db),
		publisher: &recordingOutreachPublisher{},
	}
}

func (f *stage5Fixture) service(opts ...proactive.Option) *proactive.Service {
	return proactive.NewService(f.uow, f.tasks, f.inbox, f.clock, "Europe/Moscow", opts...)
}

func (f *stage5Fixture) outreach(opts ...proactive.OutreachOption) *proactive.OutreachService {
	return proactive.NewOutreachService(f.uow, sqlite.NewOutreachAssistant(f.db), f.publisher, f.clock, "Europe/Moscow", opts...)
}

// --- detector fakes ---

type fakeCalendar struct{ events []appevents.EventWithVersion }

func (c *fakeCalendar) ListRange(_ context.Context, _, _ time.Time, _ []domainevent.Calendar) ([]appevents.EventWithVersion, error) {
	return c.events, nil
}

func meeting(title string, from, to time.Time) appevents.EventWithVersion {
	return appevents.EventWithVersion{
		Event: &domainevent.Event{
			Title:    title,
			Calendar: domainevent.CalendarWork,
			StartsAt: from,
			EndsAt:   to,
		},
		Version: 1,
	}
}

type fakeMemory struct{ agreements []proactive.Agreement }

func (m *fakeMemory) Agreements(context.Context) ([]proactive.Agreement, error) {
	return m.agreements, nil
}

type fakeGoals struct {
	stalled []appgoals.StalledGoal
	risks   []appgoals.DeadlineRisk
}

func (g *fakeGoals) Stalled(context.Context) ([]appgoals.StalledGoal, error) { return g.stalled, nil }

func (g *fakeGoals) DeadlineRisks(context.Context) ([]appgoals.DeadlineRisk, error) {
	return g.risks, nil
}

func find(insights []proactive.Insight, key string) *proactive.Insight {
	for i := range insights {
		if insights[i].Key == key {
			return &insights[i]
		}
	}
	return nil
}

func overdueInsight(t *testing.T, fixture *stage5Fixture) []proactive.Insight {
	t.Helper()
	past := fixture.clock.Now().Add(-time.Hour)
	if _, err := fixture.tasks.CreateTask(context.Background(), tasks.CreateTaskInput{
		Title: "Просрочено",
		DueAt: tasks.Some(past),
	}, task.ActorUser); err != nil {
		t.Fatalf("create task: %v", err)
	}
	insights, err := fixture.service().Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	return insights
}

// --- cross-reference detector tests ---

func TestComputeCrossReferencesCalendar(t *testing.T) {
	// The meeting runs 13:00–14:00 Moscow time; a task due 13:30 is a clash.
	from := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	calendar := &fakeCalendar{events: []appevents.EventWithVersion{meeting("Синк", from, to)}}

	fixture := newFixture(t)
	ctx := context.Background()
	clashAt := time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)
	if _, err := fixture.tasks.CreateTask(ctx, tasks.CreateTaskInput{
		Title:    "Сдать отчёт",
		DueAt:    tasks.Some(clashAt),
		Priority: tasks.Some(task.PriorityHigh),
	}, task.ActorUser); err != nil {
		t.Fatalf("create task: %v", err)
	}

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(ctx)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	insight := find(insights, "task-event-clash")
	if insight == nil {
		t.Fatalf("missing task-event-clash insight, got %v", keys(insights))
	}
	if insight.Proposal == "" {
		t.Error("an initiative needs a proposed action")
	}
}

func TestComputeBusyDay(t *testing.T) {
	base := time.Date(2026, 11, 12, 7, 0, 0, 0, time.UTC)
	calendar := &fakeCalendar{events: []appevents.EventWithVersion{
		meeting("Планирование", base, base.Add(3*time.Hour)),
		meeting("Ревью", base.Add(3*time.Hour), base.Add(6*time.Hour)),
	}}

	fixture := newFixture(t)
	ctx := context.Background()
	today, err := task.ParseCalendarDate("2026-11-12")
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	if _, err := fixture.tasks.CreateTask(ctx, tasks.CreateTaskInput{
		Title:   "Сегодня",
		DueDate: tasks.Some(today),
	}, task.ActorUser); err != nil {
		t.Fatalf("create task: %v", err)
	}

	insights, _ := fixture.service(proactive.WithCalendar(calendar)).Compute(ctx)
	if find(insights, "busy-day") == nil {
		t.Fatalf("missing busy-day insight, got %v", keys(insights))
	}
}

func tomorrowTask(t *testing.T, fixture *stage5Fixture, high bool) {
	t.Helper()
	// 2026-11-13 09:00 UTC is 12:00 Moscow time on the next day.
	tomorrow := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	input := tasks.CreateTaskInput{Title: "Важное завтра", DueAt: tasks.Some(tomorrow)}
	if high {
		input.Priority = tasks.Some(task.PriorityHigh)
	}
	if _, err := fixture.tasks.CreateTask(context.Background(), input, task.ActorUser); err != nil {
		t.Fatalf("create task: %v", err)
	}
}

func TestComputeFreeCapacityAndDueTomorrow(t *testing.T) {
	calendar := &fakeCalendar{}
	fixture := newFixture(t)
	tomorrowTask(t, fixture, true)

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	found := keys(insights)
	for _, want := range []string{"due-tomorrow", "free-capacity"} {
		if !found[want] {
			t.Errorf("missing %q insight, got %v", want, found)
		}
	}
}

func TestFreeCapacityRejectsFragmentedDay(t *testing.T) {
	// Meetings from 15:15 to 20:15 local cut the afternoon into 15-minute
	// fragments: there is no continuous window, so there is no opportunity,
	// whatever the total meeting time is.
	base := time.Date(2026, 11, 12, 12, 15, 0, 0, time.UTC)
	events := make([]appevents.EventWithVersion, 0, 7)
	for i := 0; i < 7; i++ {
		from := base.Add(time.Duration(i*45) * time.Minute)
		events = append(events, meeting("Встреча", from, from.Add(30*time.Minute)))
	}
	calendar := &fakeCalendar{events: events}

	fixture := newFixture(t)
	tomorrowTask(t, fixture, false)

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if insight := find(insights, "free-capacity"); insight != nil {
		t.Fatalf("a fragmented day must not promise a free window, got %+v", insight)
	}
}

func TestFreeCapacityRequiresOneContinuousWindow(t *testing.T) {
	// A morning meeting plus two short meetings later still leave one real
	// two-hour window between 14:00 and 18:00 local — the opportunity stands.
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	calendar := &fakeCalendar{events: []appevents.EventWithVersion{
		meeting("Утро", base, base.Add(2*time.Hour)),
		meeting("Короткая", base.Add(5*time.Hour), base.Add(5*time.Hour+30*time.Minute)),
	}}

	fixture := newFixture(t)
	tomorrowTask(t, fixture, false)

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if find(insights, "free-capacity") == nil {
		t.Fatalf("a real free window must surface the opportunity, got %v", keys(insights))
	}
}

func TestComputeForgottenAgreements(t *testing.T) {
	memory := &fakeMemory{agreements: []proactive.Agreement{
		{ID: 1, Content: "Помочь с переездом", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{ID: 2, Content: "Свежая договорённость", UpdatedAt: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)},
	}}
	fixture := newFixture(t)
	insights, err := fixture.service(proactive.WithMemory(memory)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	insight := find(insights, "forgotten-agreements")
	if insight == nil {
		t.Fatalf("missing forgotten-agreements insight, got %v", keys(insights))
	}
	if insight.Count != 1 {
		t.Errorf("only the stale agreement counts, got %d", insight.Count)
	}
}

func TestComputeGoalDeadlineRisk(t *testing.T) {
	target := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	goals := &fakeGoals{risks: []appgoals.DeadlineRisk{{
		Goal:     &domaingoal.Goal{ID: 7, Title: "Выпустить курс", TargetDate: &target},
		DaysLeft: 6, IdleDays: 5,
	}}}
	fixture := newFixture(t)
	insights, err := fixture.service(proactive.WithDeadlineRisks(goals)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if find(insights, "goal-deadline-risk-7") == nil {
		t.Fatalf("missing goal-deadline-risk insight, got %v", keys(insights))
	}
}

// --- sqlite-backed conversation helpers ---

// messages lists everything the main chat holds so far, through the real
// storage the engine writes into.
func (f *stage5Fixture) messages() ([]*domainassistant.Message, error) {
	var messages []*domainassistant.Message
	err := sqlite.NewAssistantUnitOfWork(f.db).Do(context.Background(), func(ctx context.Context, tx assistant.Transaction) error {
		found, err := tx.Assistant().ListMessages(ctx, 1, 500)
		if err != nil {
			return err
		}
		messages = found
		return nil
	})
	return messages, err
}

// addOutreachConversation seeds the main chat the way a user's first message
// would, so the engine has an existing conversation to write into.
func (f *stage5Fixture) addOutreachConversation(t *testing.T) int64 {
	t.Helper()
	var id int64
	err := sqlite.NewAssistantUnitOfWork(f.db).Do(context.Background(), func(ctx context.Context, tx assistant.Transaction) error {
		created, err := tx.Assistant().CreateConversation(ctx, domainassistant.NewConversation("Главный чат", f.clock.Now()))
		if err != nil {
			return err
		}
		id = created
		return nil
	})
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	return id
}

// addUserReply posts the user's answer into the conversation after the
// outreach message, the way a real reply would land.
func (f *stage5Fixture) addUserReply(t *testing.T, conversationID int64, content string) {
	t.Helper()
	err := sqlite.NewAssistantUnitOfWork(f.db).Do(context.Background(), func(ctx context.Context, tx assistant.Transaction) error {
		message, err := domainassistant.NewMessage(conversationID, domainassistant.RoleUser, content, f.clock.Now())
		if err != nil {
			return err
		}
		_, err = tx.Assistant().InsertMessage(ctx, message)
		return err
	})
	if err != nil {
		t.Fatalf("add reply: %v", err)
	}
}

// --- outreach gate tests ---

type recordingOutreachPublisher struct {
	events []string
}

func (p *recordingOutreachPublisher) Publish(eventType string, _ any) error {
	p.events = append(p.events, eventType)
	return nil
}

type fakeSettings struct{ values map[string]json.RawMessage }

func (s fakeSettings) Get(_ context.Context, key string) (json.RawMessage, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return value, nil
}

func TestOutreachStartsDialogueOnOwnInitiative(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()

	outreach.Evaluate(context.Background(), insights)

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one self-initiated message, got %d", len(messages))
	}
	message := messages[0]
	if message.Role != domainassistant.RoleAssistant {
		t.Fatalf("role = %q, want assistant", message.Role)
	}
	if !strings.Contains(message.Content, "Просроченные задачи") {
		t.Errorf("message must explain itself, got %q", message.Content)
	}
	var metadata struct {
		Outreach struct {
			InsightKey string `json:"insight_key"`
		} `json:"outreach"`
	}
	if err := json.Unmarshal(message.Metadata, &metadata); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if metadata.Outreach.InsightKey != "overdue" {
		t.Errorf("message must reference its insight, got %q", metadata.Outreach.InsightKey)
	}

	items, total, err := outreach.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list outreach: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one recorded outreach, got %d (total %d)", len(items), total)
	}
	if items[0].Outcome != proactive.OutreachPending {
		t.Errorf("outcome = %q, want pending", items[0].Outcome)
	}
	if items[0].MessageID != message.ID || items[0].ConversationID != message.ConversationID {
		t.Errorf("outreach must point at the chat message it created, got %+v", items[0])
	}
	if len(fixture.publisher.events) != 1 || fixture.publisher.events[0] != "outreach.created" {
		t.Errorf("events = %v, want one outreach.created", fixture.publisher.events)
	}
}

func TestOutreachRespectsQuietHours(t *testing.T) {
	fixture := newFixture(t)
	// 2026-11-12 19:30 UTC is 22:30 in Moscow — inside the night window.
	fixture.clock.Set(time.Date(2026, 11, 12, 19, 30, 0, 0, time.UTC))
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()

	outreach.Evaluate(context.Background(), insights)

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("nobody should be contacted at night, got %d messages", len(messages))
	}
}

func TestOutreachWaitsWhileUserIsBusy(t *testing.T) {
	fixture := newFixture(t)
	insights := overdueInsight(t, fixture)
	now := fixture.clock.Now()
	calendar := &fakeCalendar{events: []appevents.EventWithVersion{
		meeting("Сейчас идёт встреча", now.Add(-30*time.Minute), now.Add(30*time.Minute)),
	}}
	outreach := fixture.outreach(proactive.WithOutreachCalendar(calendar))

	outreach.Evaluate(context.Background(), insights)

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("an initiative must not interrupt a meeting, got %d messages", len(messages))
	}
}

func TestOutreachSkipsInfoInsights(t *testing.T) {
	fixture := newFixture(t)
	fixture.inbox.count = 15
	insights, err := fixture.service().Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(insights) == 0 {
		t.Fatal("expected at least one info insight")
	}
	outreach := fixture.outreach()

	outreach.Evaluate(context.Background(), insights)

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("informational observations stay in the panel, got %d messages", len(messages))
	}
}

func TestOutreachHonorsMinIntervalAndKindCooldown(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("first insight must reach out, got %d messages", len(messages))
	}

	// Ten minutes later the same situation is old news, not a second contact.
	fixture.clock.Add(10 * time.Minute)
	outreach.Evaluate(ctx, insights)
	messages, err = fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("the gate must absorb repeats inside the interval, got %d messages", len(messages))
	}

	// Two hours later the interval has passed, but the kind has not: the
	// overdue insight has already been said once today.
	fixture.clock.Add(2 * time.Hour)
	outreach.Evaluate(ctx, insights)
	messages, err = fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("the same kind must wait out its cooldown, got %d messages", len(messages))
	}

	// A different kind is allowed through as soon as the interval clears.
	goals := &fakeGoals{stalled: []appgoals.StalledGoal{{
		Goal: &domaingoal.Goal{ID: 3, Title: "Выучить английский"},
		Review: &appgoals.Review{Signals: appgoals.Signals{
			Stalled: true, StallReason: "нет записей 21 день",
		}},
	}}}
	goalInsights, err := fixture.service(proactive.WithGoals(goals)).Compute(ctx)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	outreach.Evaluate(ctx, goalInsights)
	messages, err = fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("a different kind may start a dialogue, got %d messages", len(messages))
	}
}

func TestOutreachDemotesKindAfterRepeatedDismissals(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	outreach := fixture.outreach()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		insights := overdueInsight(t, fixture)
		outreach.Evaluate(ctx, insights)
		items, _, err := outreach.List(ctx, 10, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != i+1 {
			t.Fatalf("pass %d must reach out, got %d outreaches", i+1, len(items))
		}
		if err := outreach.Dismiss(ctx, items[0].ID); err != nil {
			t.Fatalf("dismiss: %v", err)
		}
		// The next day the interval and the kind cooldown have both cleared.
		fixture.clock.Add(25 * time.Hour)
	}

	// Three dismissals inside a week teach the gate: this kind stays in the
	// panel and stops starting conversations.
	insights := overdueInsight(t, fixture)
	outreach.Evaluate(ctx, insights)
	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("a repeatedly dismissed kind must stay quiet, got %d outreaches", len(items))
	}
}

func TestOutreachSnoozeHoldsUntilTheSetTime(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	// Short cooldowns so the re-evaluation after the snooze expires is not
	// blocked by anything but the snooze itself.
	cfg := proactive.OutreachConfig{
		MinInterval:      time.Minute,
		KindCooldown:     time.Hour,
		CriticalCooldown: time.Hour,
	}
	outreach := fixture.outreach(proactive.WithOutreachConfig(cfg))
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := items[0].ID

	if err := outreach.Snooze(ctx, id, fixture.clock.Now().Add(4*time.Hour)); err != nil {
		t.Fatalf("snooze: %v", err)
	}

	// Two hours later the ordinary cooldowns have passed, but the snooze has
	// not: the topic must stay silent until the promised time.
	fixture.clock.Add(2 * time.Hour)
	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("a snoozed topic must wait out the promised time, got %d messages", len(messages))
	}

	// After the snooze expires the same insight may start a dialogue again.
	fixture.clock.Add(3 * time.Hour)
	outreach.Evaluate(ctx, insights)
	messages, err = fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("after the snooze expires the initiative may return, got %d messages", len(messages))
	}
}

func TestOutreachDisabledBySetting(t *testing.T) {
	fixture := newFixture(t)
	insights := overdueInsight(t, fixture)
	settings := fakeSettings{values: map[string]json.RawMessage{
		proactive.SettingsKey: json.RawMessage(`{"enabled": false}`),
	}}
	outreach := fixture.outreach(proactive.WithOutreachSettings(settings))

	outreach.Evaluate(context.Background(), insights)

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("the user's off switch must silence the initiative, got %d messages", len(messages))
	}
}

func TestOutreachReplyIsAcknowledgedAutomatically(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	// The user answers the initiative in the chat.
	fixture.addUserReply(t, outreachMessage.ConversationID, "Да, давай разберём")

	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachAcknowledged {
		t.Fatalf("consent must be recorded as acknowledged, got %+v", items)
	}
}

func TestOutreachReplyNotNowIsSnoozedNotAcknowledged(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	// The message itself invites "не сейчас": the reply must count as a
	// deferral, not as agreement.
	fixture.addUserReply(t, outreachMessage.ConversationID, "Не сейчас, вернусь к этому позже")
	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachSnoozed {
		t.Fatalf("a deferral must be recorded as snoozed, got %+v", items)
	}
	if items[0].SnoozedUntil == nil || !items[0].SnoozedUntil.After(fixture.clock.Now()) {
		t.Fatalf("the deferral must hold into the future, got %+v", items[0])
	}
}

func TestOutreachReplyRefusalIsDismissed(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	fixture.addUserReply(t, outreachMessage.ConversationID, "Нет, не надо этим заниматься")
	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachDismissed {
		t.Fatalf("a refusal must be recorded as dismissed, got %+v", items)
	}
}

func TestOutreachReactionsUpdateOutcome(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := items[0].ID

	if err := outreach.Snooze(ctx, id, fixture.clock.Now().Add(4*time.Hour)); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	found, err := outreach.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.Outcome != proactive.OutreachSnoozed || found.SnoozedUntil == nil {
		t.Fatalf("snooze must be recorded, got %+v", found)
	}

	// A decided outreach does not change its mind through a second decision.
	if err := outreach.Acknowledge(ctx, id); err == nil {
		t.Fatal("a decided outreach must not be decided twice")
	}
}

func TestOutreachAtomicWriteLeavesNothingBehind(t *testing.T) {
	fixture := newFixture(t)

	// The message and the outreach record must commit together: a failure
	// after the message insert rolls the whole contact back, so the next
	// pass cannot double the initiative.
	err := sqlite.NewOutreachAssistant(fixture.db).Do(context.Background(), func(ctx context.Context, tx proactive.AssistantTx) error {
		conversation := domainassistant.NewConversation("Главный чат", fixture.clock.Now())
		if _, err := tx.CreateConversation(ctx, conversation); err != nil {
			return err
		}
		message, err := domainassistant.NewMessage(conversation.ID, domainassistant.RoleAssistant, "Проверка", fixture.clock.Now())
		if err != nil {
			return err
		}
		if _, err := tx.InsertMessage(ctx, message); err != nil {
			return err
		}
		if _, err := tx.CreateOutreach(ctx, &proactive.Outreach{
			ConversationID: conversation.ID,
			MessageID:      message.ID,
			InsightKey:     "overdue",
			Kind:           "overdue",
			Severity:       proactive.SeverityCritical,
			Title:          "Проверка",
			CreatedAt:      fixture.clock.Now(),
			Outcome:        proactive.OutreachPending,
		}); err != nil {
			return err
		}
		return errors.New("simulated failure after both writes")
	})
	if err == nil {
		t.Fatal("expected the simulated failure to surface")
	}

	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("a failed contact must not leave the message behind, got %d messages", len(messages))
	}
	outreach := fixture.outreach()
	items, total, err := outreach.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("a failed contact must not leave a history entry, got %+v", items)
	}
}

func TestFreeCapacityIgnoresMeetingsAfterUsableDay(t *testing.T) {
	// It is 20:00 local, the usable day ends at 21:00, and the next meeting
	// starts at 23:00: only the hour that remains is free, whatever the
	// unclamped arithmetic between now and the meeting says.
	late := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC) // 23:00 Moscow
	calendar := &fakeCalendar{events: []appevents.EventWithVersion{
		meeting("Ночная встреча", late, late.Add(30*time.Minute)),
	}}

	fixture := newFixture(t)
	// 17:00 UTC is 20:00 in Moscow.
	fixture.clock.Set(time.Date(2026, 11, 12, 17, 0, 0, 0, time.UTC))
	tomorrowTask(t, fixture, false)

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(context.Background())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if insight := find(insights, "free-capacity"); insight != nil {
		t.Fatalf("time beyond the usable day is not a free window, got %+v", insight)
	}
}

func TestOutreachReplyNotNowEnglishIsSnoozed(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	// "not now" contains "no" as a substring: the reply must still be a
	// deferral, not a refusal.
	fixture.addUserReply(t, outreachMessage.ConversationID, "Not now, I am busy")
	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachSnoozed {
		t.Fatalf("an English deferral must be snoozed, got %+v", items)
	}
}

func TestOutreachReplyBareNoIsDismissed(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	fixture.addUserReply(t, outreachMessage.ConversationID, "Нет")
	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachDismissed {
		t.Fatalf("a bare no must be dismissed, got %+v", items)
	}
}

func TestOutreachReplyQuestionStaysPending(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	outreachMessage := messages[0]

	// A question or an unrelated message is not a decision: the outreach
	// stays pending instead of being silently accepted.
	fixture.addUserReply(t, outreachMessage.ConversationID, "Что ты имеешь в виду?")
	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachPending {
		t.Fatalf("an unrecognized reply must not count as agreement, got %+v", items)
	}
}

func TestOutreachLongSnoozeOutlivesLookback(t *testing.T) {
	fixture := newFixture(t)
	fixture.addOutreachConversation(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := items[0].ID

	// The user pushes the topic a month away; the statistics window is a
	// week. The hold must outlive the window.
	if err := outreach.Snooze(ctx, id, fixture.clock.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	fixture.clock.Add(8 * 24 * time.Hour)

	outreach.Evaluate(ctx, insights)
	messages, err := fixture.messages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("a month-long snooze must outlive the week lookback, got %d messages", len(messages))
	}
}
