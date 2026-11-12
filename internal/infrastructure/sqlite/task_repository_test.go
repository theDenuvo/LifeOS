package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

func newTaskTestService(t *testing.T, now time.Time) (*tasks.Service, *TaskUnitOfWork) {
	t.Helper()

	db, err := Open(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	uow := NewTaskUnitOfWork(db)
	service := tasks.NewService(uow, fixedClock{now: now}, tasks.WithDefaultTimezone("Europe/Moscow"))
	return service, uow
}

func mustCreateTask(t *testing.T, service *tasks.Service, in tasks.CreateTaskInput) tasks.TaskWithVersion {
	t.Helper()
	created, err := service.CreateTask(context.Background(), in, task.ActorUser)
	if err != nil {
		t.Fatalf("create task %q: %v", in.Title, err)
	}
	return created
}

func TestTaskRepositoryCreateGetAndEvent(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, uow := newTaskTestService(t, now)

	planned, _ := task.ParseCalendarDate("2026-11-12")
	dueDate, _ := task.ParseCalendarDate("2026-11-13")
	dueAt := time.Date(2026, 11, 13, 18, 30, 0, 0, time.UTC)

	created := mustCreateTask(t, service, tasks.CreateTaskInput{
		Title:       "Подготовить презентацию",
		Note:        tasks.Some("Слайды 1-15"),
		Priority:    tasks.Some(task.PriorityHigh),
		PlannedDate: tasks.Some(planned),
		DueDate:     tasks.Some(dueDate),
		DueAt:       tasks.Some(dueAt),
		Tags:        []string{"работа", "совет"},
	})

	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}
	if created.Task.ID == 0 {
		t.Fatal("created task has no id")
	}

	found, err := service.GetTask(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	got := found.Task
	if got.Title != "Подготовить презентацию" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Note == nil || *got.Note != "Слайды 1-15" {
		t.Errorf("note = %v", got.Note)
	}
	if got.Priority != task.PriorityHigh {
		t.Errorf("priority = %q", got.Priority)
	}
	if got.Status != task.StatusOpen {
		t.Errorf("status = %q", got.Status)
	}
	if got.PlannedDate == nil || got.PlannedDate.String() != "2026-11-12" {
		t.Errorf("planned date = %v", got.PlannedDate)
	}
	if got.DueDate == nil || got.DueDate.String() != "2026-11-13" {
		t.Errorf("due date = %v", got.DueDate)
	}
	if got.DueAt == nil || !got.DueAt.Equal(dueAt) {
		t.Errorf("due at = %v, want %v", got.DueAt, dueAt)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "работа" {
		t.Errorf("tags = %v", got.Tags)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("timestamps = %v / %v, want %v", got.CreatedAt, got.UpdatedAt, now)
	}

	var eventType, actor, payload string
	var eventAt int64
	if err := uow.db.QueryRow(`SELECT type, actor, payload, created_at FROM task_events WHERE task_id = ?`,
		created.Task.ID).Scan(&eventType, &actor, &payload, &eventAt); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if eventType != "created" || actor != "user" {
		t.Errorf("event = %s/%s, want created/user", eventType, actor)
	}
	if eventAt != now.Unix() {
		t.Errorf("event timestamp = %d, want %d", eventAt, now.Unix())
	}
	if payload == "" {
		t.Error("event payload is empty")
	}
}

func TestTaskRepositoryOptimisticLocking(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, _ := newTaskTestService(t, now)
	created := mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Задача"})

	updated, err := service.UpdateTask(context.Background(), created.Task.ID, created.Version,
		tasks.UpdateTaskInput{Title: tasks.Some("Задача обновлена")}, task.ActorUser)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version after update = %d, want 2", updated.Version)
	}
	if updated.Task.Title != "Задача обновлена" {
		t.Errorf("title = %q", updated.Task.Title)
	}

	_, err = service.UpdateTask(context.Background(), created.Task.ID, created.Version,
		tasks.UpdateTaskInput{Title: tasks.Some("Устаревшая запись")}, task.ActorUser)
	if !errors.Is(err, tasks.ErrVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrVersionConflict", err)
	}

	current, err := service.GetTask(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.Task.Title != "Задача обновлена" {
		t.Errorf("stale write changed title to %q", current.Task.Title)
	}
	if current.Version != 2 {
		t.Errorf("version = %d, want 2", current.Version)
	}

	_, err = service.UpdateTask(context.Background(), 9999, 1,
		tasks.UpdateTaskInput{Title: tasks.Some("Нет такой")}, task.ActorUser)
	if !errors.Is(err, tasks.ErrTaskNotFound) {
		t.Fatalf("missing task error = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskRepositoryStatusTransitionsAndEvents(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, uow := newTaskTestService(t, now)
	created := mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Позвонить"})

	completed, err := service.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorAssistant)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Task.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", completed.Task.Status)
	}
	if completed.Task.CompletedAt == nil || !completed.Task.CompletedAt.Equal(now) {
		t.Errorf("completed_at = %v, want %v", completed.Task.CompletedAt, now)
	}
	if completed.Version != 2 {
		t.Errorf("version = %d, want 2", completed.Version)
	}

	if _, err := service.CancelTask(context.Background(), created.Task.ID, completed.Version, task.ActorUser); err == nil {
		t.Fatal("cancelling a completed task should fail")
	} else if !task.IsInvalidTransitionError(err) {
		t.Fatalf("cancel error = %v, want InvalidTransitionError", err)
	}

	restored, err := service.RestoreTask(context.Background(), created.Task.ID, completed.Version, task.ActorUser)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Task.Status != task.StatusOpen || restored.Task.CompletedAt != nil {
		t.Errorf("restore left status=%q completed_at=%v", restored.Task.Status, restored.Task.CompletedAt)
	}

	var createdCount, completedCount, restoredCount int
	if err := uow.db.QueryRow(
		`SELECT SUM(type='created'), SUM(type='completed'), SUM(type='restored') FROM task_events WHERE task_id = ?`,
		created.Task.ID).Scan(&createdCount, &completedCount, &restoredCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if createdCount != 1 || completedCount != 1 || restoredCount != 1 {
		t.Errorf("events = created:%d completed:%d restored:%d", createdCount, completedCount, restoredCount)
	}
}

func TestTaskRepositoryRollsBackOnError(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, uow := newTaskTestService(t, now)

	sentinel := errors.New("boom")
	err := uow.Do(context.Background(), func(ctx context.Context, tx tasks.Transaction) error {
		created, err := task.NewTask("Откат", task.PriorityMedium, "Europe/Moscow", now)
		if err != nil {
			return err
		}
		if _, err := tx.Tasks().Insert(ctx, created); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("unit of work error = %v, want sentinel", err)
	}

	result, err := service.ListTasks(context.Background(), tasks.TaskFilter{Search: tasks.Some("Откат")})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Total != 0 || len(result.Tasks) != 0 {
		t.Fatalf("rollback left %d tasks", result.Total)
	}
}

func TestTaskRepositoryListFilteringAndPagination(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, _ := newTaskTestService(t, now)

	past := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	future := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	yesterday, _ := task.ParseCalendarDate("2026-11-11")
	today, _ := task.ParseCalendarDate("2026-11-12")
	longAgo, _ := task.ParseCalendarDate("2026-11-01")

	mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Просрочено точно", DueAt: tasks.Some(past), Priority: tasks.Some(task.PriorityHigh)})
	mustCreateTask(t, service, tasks.CreateTaskInput{Title: "В срок точно", DueAt: tasks.Some(future), Priority: tasks.Some(task.PriorityLow)})
	mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Просрочено календарно", DueDate: tasks.Some(yesterday)})
	mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Сегодня календарно", DueDate: tasks.Some(today), Tags: []string{"дом"}})
	completed := mustCreateTask(t, service, tasks.CreateTaskInput{Title: "Выполнено давно", DueDate: tasks.Some(longAgo)})

	if _, err := service.CompleteTask(context.Background(), completed.Task.ID, completed.Version, task.ActorUser); err != nil {
		t.Fatalf("complete: %v", err)
	}

	assertTitles := func(label string, filter tasks.TaskFilter, want ...string) {
		t.Helper()
		result, err := service.ListTasks(context.Background(), filter)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if result.Total != len(want) {
			t.Fatalf("%s: total = %d, want %d", label, result.Total, len(want))
		}
		got := make(map[string]bool, len(result.Tasks))
		for _, item := range result.Tasks {
			got[item.Task.Title] = true
		}
		for _, title := range want {
			if !got[title] {
				t.Errorf("%s: missing %q (got %v)", label, title, got)
			}
		}
	}

	assertTitles("overdue=true", tasks.TaskFilter{Overdue: tasks.Some(true)},
		"Просрочено точно", "Просрочено календарно")
	assertTitles("overdue=false", tasks.TaskFilter{Overdue: tasks.Some(false)},
		"В срок точно", "Сегодня календарно", "Выполнено давно")
	assertTitles("status=completed", tasks.TaskFilter{Statuses: []task.Status{task.StatusCompleted}},
		"Выполнено давно")
	assertTitles("priority=high", tasks.TaskFilter{Priorities: []task.Priority{task.PriorityHigh}},
		"Просрочено точно")
	assertTitles("search", tasks.TaskFilter{Search: tasks.Some("календарно")},
		"Просрочено календарно", "Сегодня календарно")
	assertTitles("tag", tasks.TaskFilter{Tags: []string{"дом"}}, "Сегодня календарно")
	assertTitles("due_date", tasks.TaskFilter{DueDate: tasks.Some(today)}, "Сегодня календарно")

	all, err := service.ListTasks(context.Background(), tasks.TaskFilter{Limit: tasks.Some(2), Offset: tasks.Some(0)})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if all.Total != 5 || len(all.Tasks) != 2 {
		t.Errorf("page 1 = total %d, len %d", all.Total, len(all.Tasks))
	}
	second, err := service.ListTasks(context.Background(), tasks.TaskFilter{Limit: tasks.Some(2), Offset: tasks.Some(2)})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second.Tasks) != 2 {
		t.Errorf("page 2 len = %d, want 2", len(second.Tasks))
	}
	if all.Tasks[0].Task.Title == second.Tasks[0].Task.Title {
		t.Error("pagination returned the same first row")
	}

	// Sorting by title is case-insensitive and deterministic.
	sorted, err := service.ListTasks(context.Background(), tasks.TaskFilter{
		Sorts: []tasks.TaskSort{{Field: tasks.SortTitle, Order: tasks.SortAsc}},
	})
	if err != nil {
		t.Fatalf("sorted list: %v", err)
	}
	if len(sorted.Tasks) != 5 {
		t.Fatalf("sorted list len = %d", len(sorted.Tasks))
	}

	// The overdue filter paginates after filtering, not before.
	overduePage, err := service.ListTasks(context.Background(), tasks.TaskFilter{
		Overdue: tasks.Some(true), Limit: tasks.Some(1), Offset: tasks.Some(1),
	})
	if err != nil {
		t.Fatalf("overdue page: %v", err)
	}
	if overduePage.Total != 2 || len(overduePage.Tasks) != 1 {
		t.Errorf("overdue page = total %d len %d, want 2/1", overduePage.Total, len(overduePage.Tasks))
	}
}

func TestTaskRepositoryClearsFields(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	service, uow := newTaskTestService(t, now)
	dueAt := now.Add(48 * time.Hour)
	created := mustCreateTask(t, service, tasks.CreateTaskInput{
		Title: "Очистить поля",
		Note:  tasks.Some("заметка"),
		DueAt: tasks.Some(dueAt),
		Tags:  []string{"a"},
	})

	updated, err := service.UpdateTask(context.Background(), created.Task.ID, created.Version, tasks.UpdateTaskInput{
		Note:  tasks.Some(""),
		DueAt: tasks.Some(time.Time{}),
		Tags:  tasks.Some([]string{}),
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("clear update: %v", err)
	}
	if updated.Task.Note != nil {
		t.Errorf("note = %v, want nil", updated.Task.Note)
	}
	if updated.Task.DueAt != nil {
		t.Errorf("due_at = %v, want nil", updated.Task.DueAt)
	}
	if len(updated.Task.Tags) != 0 {
		t.Errorf("tags = %v, want empty", updated.Task.Tags)
	}

	// A clear is a real change: it bumps the version and writes an event.
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	var updatedEvents int
	if err := uow.db.QueryRow(
		`SELECT COUNT(*) FROM task_events WHERE task_id = ? AND type = 'updated'`,
		created.Task.ID).Scan(&updatedEvents); err != nil {
		t.Fatalf("count updated events: %v", err)
	}
	if updatedEvents != 1 {
		t.Errorf("updated events = %d, want 1", updatedEvents)
	}
}
