package proactive_test

import (
	"context"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func setup(t *testing.T) (*proactive.Service, *tasks.Service, *inbox.Service, *sqlite.InsightUnitOfWork) {
	t.Helper()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	db, err := sqlite.Open(t.TempDir() + "/proactive.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	clock := fixedClock{now: now}
	taskService := tasks.NewService(sqlite.NewTaskUnitOfWork(db), clock, tasks.WithDefaultTimezone("Europe/Moscow"))
	inboxService := inbox.NewService(sqlite.NewInboxUnitOfWork(db), clock, "Europe/Moscow")
	uow := sqlite.NewInsightUnitOfWork(db)
	service := proactive.NewService(uow, taskService, inboxService, clock, "Europe/Moscow")
	return service, taskService, inboxService, uow
}

func create(t *testing.T, service *tasks.Service, input tasks.CreateTaskInput) {
	t.Helper()
	if _, err := service.CreateTask(context.Background(), input, task.ActorUser); err != nil {
		t.Fatalf("create %q: %v", input.Title, err)
	}
}

func keys(insights []proactive.Insight) map[string]bool {
	result := make(map[string]bool, len(insights))
	for _, insight := range insights {
		result[insight.Key] = true
	}
	return result
}

func TestComputeDetectsProblems(t *testing.T) {
	service, taskService, inboxService, _ := setup(t)
	ctx := context.Background()

	past := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	create(t, taskService, tasks.CreateTaskInput{Title: "Просрочено", DueAt: tasks.Some(past)})

	today, _ := task.ParseCalendarDate("2026-11-12")
	for i := 0; i < 8; i++ {
		create(t, taskService, tasks.CreateTaskInput{Title: "Сегодня", DueDate: tasks.Some(today)})
	}

	sameTime := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	create(t, taskService, tasks.CreateTaskInput{Title: "Встреча A", DueAt: tasks.Some(sameTime)})
	create(t, taskService, tasks.CreateTaskInput{Title: "Встреча B", DueAt: tasks.Some(sameTime)})

	for i := 0; i < 11; i++ {
		if _, err := inboxService.Capture(ctx, "мысль", nil); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}

	insights, err := service.Compute(ctx)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	found := keys(insights)
	for _, want := range []string{"overdue", "due-today", "overload", "inbox-backlog", "duplicate-deadline"} {
		if !found[want] {
			t.Errorf("missing insight %q (got %v)", want, found)
		}
	}
	if len(insights) > 0 && insights[0].Severity != proactive.SeverityCritical {
		t.Errorf("first insight severity = %q, want critical", insights[0].Severity)
	}
}

func TestDismissAndSnoozeFilterInsights(t *testing.T) {
	service, taskService, _, _ := setup(t)
	ctx := context.Background()

	past := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	create(t, taskService, tasks.CreateTaskInput{Title: "Просрочено", DueAt: tasks.Some(past)})

	insights, err := service.Compute(ctx)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if !keys(insights)["overdue"] {
		t.Fatal("overdue insight missing")
	}

	if err := service.Dismiss(ctx, "overdue"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	insights, err = service.Compute(ctx)
	if err != nil {
		t.Fatalf("compute after dismiss: %v", err)
	}
	if keys(insights)["overdue"] {
		t.Error("dismissed insight is still visible")
	}

	// Snooze in the past does not hide; snooze in the future does.
	if err := service.Snooze(ctx, "overdue", time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("snooze past: %v", err)
	}
	insights, _ = service.Compute(ctx)
	if !keys(insights)["overdue"] {
		t.Error("expired snooze still hides the insight")
	}

	if err := service.Snooze(ctx, "overdue", time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("snooze future: %v", err)
	}
	insights, _ = service.Compute(ctx)
	if keys(insights)["overdue"] {
		t.Error("snoozed insight is still visible")
	}
}

type recordingPublisher struct{ keys []string }

func (p *recordingPublisher) Publish(_ string, data any) {
	if payload, ok := data.(map[string]any); ok {
		if key, ok := payload["key"].(string); ok {
			p.keys = append(p.keys, key)
		}
	}
}

func TestRunnerPublishesOnlyNewInsights(t *testing.T) {
	service, taskService, _, _ := setup(t)
	ctx := context.Background()

	past := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	create(t, taskService, tasks.CreateTaskInput{Title: "Просрочено", DueAt: tasks.Some(past)})

	publisher := &recordingPublisher{}
	runner := proactive.NewRunner(service, publisher, time.Hour)
	runner.RunOnce(ctx)
	runner.RunOnce(ctx)

	if len(publisher.keys) != 1 || publisher.keys[0] != "overdue" {
		t.Errorf("published = %v, want one overdue", publisher.keys)
	}
}
