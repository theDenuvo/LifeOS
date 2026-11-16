package proactive_test

// Roadmap stage 5: cross-referenced detectors and the initiative gate.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	clock     *mutableClock
	tasks     *tasks.Service
	inbox     *fakeInbox
	uow       *sqlite.InsightUnitOfWork
	assistant *fakeAssistant
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
	fixture := &stage5Fixture{
		clock:     clock,
		tasks:     tasks.NewService(sqlite.NewTaskUnitOfWork(db), clock, tasks.WithDefaultTimezone("Europe/Moscow")),
		inbox:     &fakeInbox{},
		uow:       sqlite.NewInsightUnitOfWork(db),
		assistant: &fakeAssistant{},
		publisher: &recordingOutreachPublisher{},
	}
	return fixture
}

func (f *stage5Fixture) service(opts ...proactive.Option) *proactive.Service {
	return proactive.NewService(f.uow, f.tasks, f.inbox, f.clock, "Europe/Moscow", opts...)
}

func (f *stage5Fixture) outreach(opts ...proactive.OutreachOption) *proactive.OutreachService {
	return proactive.NewOutreachService(f.uow, f.assistant, f.publisher, f.clock, "Europe/Moscow", opts...)
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

func TestComputeFreeCapacityAndDueTomorrow(t *testing.T) {
	calendar := &fakeCalendar{}
	fixture := newFixture(t)
	ctx := context.Background()

	// 2026-11-13 09:00 UTC is 12:00 Moscow time on the next day.
	tomorrow := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if _, err := fixture.tasks.CreateTask(ctx, tasks.CreateTaskInput{
		Title:    "Важное завтра",
		DueAt:    tasks.Some(tomorrow),
		Priority: tasks.Some(task.PriorityHigh),
	}, task.ActorUser); err != nil {
		t.Fatalf("create task: %v", err)
	}

	insights, err := fixture.service(proactive.WithCalendar(calendar)).Compute(ctx)
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

// --- fake assistant storage ---

type fakeAssistant struct {
	mu            sync.Mutex
	conversations []*domainassistant.Conversation
	messages      []*domainassistant.Message
	nextID        int64
}

func (f *fakeAssistant) Do(_ context.Context, fn func(ctx context.Context, tx proactive.AssistantTx) error) error {
	return fn(context.Background(), f)
}

func (f *fakeAssistant) CreateConversation(_ context.Context, conversation *domainassistant.Conversation) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	conversation.ID = f.nextID
	f.conversations = append(f.conversations, conversation)
	return conversation.ID, nil
}

func (f *fakeAssistant) GetConversation(_ context.Context, id int64) (*domainassistant.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, conversation := range f.conversations {
		if conversation.ID == id {
			return conversation, nil
		}
	}
	return nil, errors.New("conversation not found")
}

func (f *fakeAssistant) ListConversations(_ context.Context, limit, offset int, kind *domainassistant.Kind) ([]*domainassistant.Conversation, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]*domainassistant.Conversation, 0, len(f.conversations))
	for _, conversation := range f.conversations {
		if kind != nil && conversation.Kind != *kind {
			continue
		}
		result = append(result, conversation)
	}
	total := len(result)
	if offset < total {
		result = result[offset:]
	} else {
		result = nil
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, total, nil
}

func (f *fakeAssistant) TouchConversation(_ context.Context, id int64, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, conversation := range f.conversations {
		if conversation.ID == id {
			conversation.UpdatedAt = at
		}
	}
	return nil
}

func (f *fakeAssistant) InsertMessage(_ context.Context, message *domainassistant.Message) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	message.ID = f.nextID
	f.messages = append(f.messages, message)
	return message.ID, nil
}

func (f *fakeAssistant) ListMessages(_ context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]*domainassistant.Message, 0, len(f.messages))
	for _, message := range f.messages {
		if message.ConversationID == conversationID {
			result = append(result, message)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

func (f *fakeAssistant) userMessages() []*domainassistant.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]*domainassistant.Message, 0)
	for _, message := range f.messages {
		if message.Role == domainassistant.RoleUser {
			result = append(result, message)
		}
	}
	return result
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
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()

	outreach.Evaluate(context.Background(), insights)

	messages := fixture.assistant.messages
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

	if len(fixture.assistant.messages) != 0 {
		t.Errorf("nobody should be contacted at night, got %d messages", len(fixture.assistant.messages))
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

	if len(fixture.assistant.messages) != 0 {
		t.Errorf("an initiative must not interrupt a meeting, got %d messages", len(fixture.assistant.messages))
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

	if len(fixture.assistant.messages) != 0 {
		t.Errorf("informational observations stay in the panel, got %d messages", len(fixture.assistant.messages))
	}
}

func TestOutreachHonorsMinIntervalAndKindCooldown(t *testing.T) {
	fixture := newFixture(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	if len(fixture.assistant.messages) != 1 {
		t.Fatalf("first insight must reach out, got %d messages", len(fixture.assistant.messages))
	}

	// Ten minutes later the same situation is old news, not a second contact.
	fixture.clock.Add(10 * time.Minute)
	outreach.Evaluate(ctx, insights)
	if len(fixture.assistant.messages) != 1 {
		t.Fatalf("the gate must absorb repeats inside the interval, got %d messages", len(fixture.assistant.messages))
	}

	// Two hours later the interval has passed, but the kind has not: the
	// overdue insight has already been said once today.
	fixture.clock.Add(2 * time.Hour)
	outreach.Evaluate(ctx, insights)
	if len(fixture.assistant.messages) != 1 {
		t.Fatalf("the same kind must wait out its cooldown, got %d messages", len(fixture.assistant.messages))
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
	if len(fixture.assistant.messages) != 2 {
		t.Fatalf("a different kind may start a dialogue, got %d messages", len(fixture.assistant.messages))
	}
}

func TestOutreachDemotesKindAfterRepeatedDismissals(t *testing.T) {
	fixture := newFixture(t)
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

func TestOutreachDisabledBySetting(t *testing.T) {
	fixture := newFixture(t)
	insights := overdueInsight(t, fixture)
	settings := fakeSettings{values: map[string]json.RawMessage{
		proactive.SettingsKey: json.RawMessage(`{"enabled": false}`),
	}}
	outreach := fixture.outreach(proactive.WithOutreachSettings(settings))

	outreach.Evaluate(context.Background(), insights)

	if len(fixture.assistant.messages) != 0 {
		t.Errorf("the user's off switch must silence the initiative, got %d messages", len(fixture.assistant.messages))
	}
}

func TestOutreachReplyIsAcknowledgedAutomatically(t *testing.T) {
	fixture := newFixture(t)
	insights := overdueInsight(t, fixture)
	outreach := fixture.outreach()
	ctx := context.Background()

	outreach.Evaluate(ctx, insights)
	outreachMessage := fixture.assistant.messages[0]

	// The user answers the initiative in the chat.
	if _, err := fixture.assistant.InsertMessage(ctx, &domainassistant.Message{
		ConversationID: outreachMessage.ConversationID,
		Role:           domainassistant.RoleUser,
		Content:        "Да, давай разберём",
		CreatedAt:      fixture.clock.Now(),
	}); err != nil {
		t.Fatalf("reply: %v", err)
	}

	outreach.AcknowledgeReplies(ctx)

	items, _, err := outreach.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Outcome != proactive.OutreachAcknowledged {
		t.Fatalf("a reply is the strongest reaction, got %+v", items)
	}
}

func TestOutreachReactionsUpdateOutcome(t *testing.T) {
	fixture := newFixture(t)
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
