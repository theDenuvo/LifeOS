package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// fixedClock is a deterministic Clock for tests.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

var testNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, opts ...ServiceOption) (*Service, *FakeUnitOfWork) {
	t.Helper()
	uow := NewFakeUnitOfWork()
	return NewService(uow, fixedClock{now: testNow}, opts...), uow
}

// --- CreateTask ---------------------------------------------------------------

func TestCreateTask_Defaults(t *testing.T) {
	svc, uow := newTestService(t)

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "Купить молоко"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if created.Task.Priority != DefaultPriority {
		t.Errorf("Priority = %v, want default %v", created.Task.Priority, DefaultPriority)
	}
	if created.Task.Timezone != DefaultTimezone {
		t.Errorf("Timezone = %q, want default %q", created.Task.Timezone, DefaultTimezone)
	}
	if created.Version != 1 {
		t.Errorf("Version = %d, want 1", created.Version)
	}
	if created.Task.ID == 0 {
		t.Error("ID must be assigned after insert")
	}
	if created.Task.Status != task.StatusOpen {
		t.Errorf("Status = %v, want open", created.Task.Status)
	}

	events := uow.Events()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].Type != task.EventTypeCreated {
		t.Errorf("event type = %v, want created", events[0].Type)
	}
	if events[0].Actor != task.ActorUser {
		t.Errorf("event actor = %v, want user", events[0].Actor)
	}
	if events[0].TaskID != created.Task.ID {
		t.Errorf("event task_id = %d, want %d", events[0].TaskID, created.Task.ID)
	}
	// Task and event share one instant from the clock.
	if !events[0].CreatedAt.Equal(created.Task.CreatedAt) {
		t.Errorf("event time %v != task time %v", events[0].CreatedAt, created.Task.CreatedAt)
	}
	if !created.Task.CreatedAt.Equal(testNow) {
		t.Errorf("task time = %v, want clock value %v", created.Task.CreatedAt, testNow)
	}
}

func TestCreateTask_CustomTimezone(t *testing.T) {
	svc, _ := newTestService(t, WithDefaultTimezone("America/New_York"))

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "Call"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if created.Task.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q, want America/New_York", created.Task.Timezone)
	}
}

func TestCreateTask_ExplicitValues(t *testing.T) {
	svc, _ := newTestService(t)

	due := task.CalendarDate{Year: 2026, Month: 12, Day: 1}
	dueAt := testNow.Add(48 * time.Hour)

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "Срочная задача",
		Note:     Some("важная заметка"),
		Priority: Some(task.PriorityHigh),
		DueDate:  Some(due),
		DueAt:    Some(dueAt),
		Timezone: Some("Europe/Berlin"),
		Tags:     []string{"работа", "срочно"},
	}, task.ActorAssistant)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if created.Task.Title != "Срочная задача" {
		t.Errorf("Title = %q", created.Task.Title)
	}
	if created.Task.Note == nil || *created.Task.Note != "важная заметка" {
		t.Error("Note not set")
	}
	if created.Task.Priority != task.PriorityHigh {
		t.Errorf("Priority = %v, want high", created.Task.Priority)
	}
	if created.Task.DueDate == nil || *created.Task.DueDate != due {
		t.Error("DueDate not set")
	}
	if created.Task.DueAt == nil || !created.Task.DueAt.Equal(dueAt) {
		t.Error("DueAt not set")
	}
	if created.Task.Timezone != "Europe/Berlin" {
		t.Errorf("Timezone = %q", created.Task.Timezone)
	}
	if len(created.Task.Tags) != 2 {
		t.Errorf("Tags = %v", created.Task.Tags)
	}
}

func TestCreateTask_DomainValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		input CreateTaskInput
		want  error
	}{
		{"empty title", CreateTaskInput{Title: ""}, task.ErrInvalidTitle},
		{"bad priority", CreateTaskInput{Title: "x", Priority: Some(task.Priority("urgent"))}, task.ErrInvalidPriority},
		{"bad timezone", CreateTaskInput{Title: "x", Timezone: Some("Mars/Olympus")}, task.ErrInvalidTimezone},
		{"impossible due date", CreateTaskInput{Title: "x", DueDate: Some(task.CalendarDate{Year: 2026, Month: 2, Day: 30})}, task.ErrInvalidDueDate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newTestService(t)
			_, err := svc.CreateTask(context.Background(), tt.input, task.ActorUser)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if len(uow.Tasks()) != 0 {
				t.Error("failed create must not persist a task")
			}
			if uow.EventCount() != 0 {
				t.Error("failed create must not persist an event")
			}
		})
	}
}

func TestCreateTask_InvalidActor(t *testing.T) {
	svc, uow := newTestService(t)
	_, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.Actor("robot"))
	if !errors.Is(err, task.ErrInvalidActor) {
		t.Fatalf("error = %v, want ErrInvalidActor", err)
	}
	if uow.EventCount() != 0 {
		t.Error("no event must be written for an invalid actor")
	}
}

// --- GetTask ------------------------------------------------------------------

func TestGetTask_Found(t *testing.T) {
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	found, err := svc.GetTask(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if found.Task.ID != created.Task.ID {
		t.Errorf("ID = %d, want %d", found.Task.ID, created.Task.ID)
	}
	if found.Version != 1 {
		t.Errorf("Version = %d, want 1", found.Version)
	}

	// Reading must not change anything.
	after := uow.FindTaskMust(t, created.Task.ID)
	if after.Version != 1 {
		t.Errorf("read bumped version to %d", after.Version)
	}
	if uow.EventCount() != 1 {
		t.Errorf("read wrote an event: count = %d", uow.EventCount())
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.GetTask(context.Background(), 999)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("error = %v, want ErrTaskNotFound", err)
	}
	if !NotFound(err) {
		t.Error("NotFound(err) must report true")
	}
}

// --- ListTasks ----------------------------------------------------------------

func TestListTasks_Defaults(t *testing.T) {
	svc, _ := newTestService(t)
	for _, title := range []string{"a", "b", "c"} {
		if _, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: title}, task.ActorUser); err != nil {
			t.Fatalf("CreateTask failed: %v", err)
		}
	}

	result, err := svc.ListTasks(context.Background(), TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if result.Limit != DefaultPageLimit {
		t.Errorf("Limit = %d, want %d", result.Limit, DefaultPageLimit)
	}
	if result.Offset != 0 {
		t.Errorf("Offset = %d, want 0", result.Offset)
	}
	if result.Total != 3 {
		t.Errorf("Total = %d, want 3", result.Total)
	}
	if len(result.Tasks) != 3 {
		t.Errorf("len(Tasks) = %d, want 3", len(result.Tasks))
	}
}

func TestListTasks_Pagination(t *testing.T) {
	svc, _ := newTestService(t)
	for _, title := range []string{"t1", "t2", "t3", "t4", "t5"} {
		if _, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: title}, task.ActorUser); err != nil {
			t.Fatalf("CreateTask failed: %v", err)
		}
	}

	first, err := svc.ListTasks(context.Background(), TaskFilter{
		Limit:  Some(2),
		Offset: Some(0),
	})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(first.Tasks) != 2 || first.Total != 5 {
		t.Fatalf("page 1: len=%d total=%d", len(first.Tasks), first.Total)
	}

	second, err := svc.ListTasks(context.Background(), TaskFilter{
		Limit:  Some(2),
		Offset: Some(2),
	})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(second.Tasks) != 2 || second.Total != 5 {
		t.Fatalf("page 2: len=%d total=%d", len(second.Tasks), second.Total)
	}

	// Pages must not overlap.
	if first.Tasks[0].Task.ID == second.Tasks[0].Task.ID {
		t.Error("pages overlap")
	}

	// Offset beyond the end yields an empty page but the same total.
	last, err := svc.ListTasks(context.Background(), TaskFilter{
		Limit:  Some(10),
		Offset: Some(99),
	})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(last.Tasks) != 0 || last.Total != 5 {
		t.Errorf("beyond end: len=%d total=%d", len(last.Tasks), last.Total)
	}
}

func TestListTasks_InvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		filter TaskFilter
		want   error
	}{
		{"limit 0", TaskFilter{Limit: Some(0)}, ErrInvalidLimit},
		{"limit 101", TaskFilter{Limit: Some(101)}, ErrInvalidLimit},
		{"negative offset", TaskFilter{Offset: Some(-1)}, ErrInvalidOffset},
		{"bad sort field", TaskFilter{Sorts: []TaskSort{{Field: TaskSortField("bogus"), Order: SortAsc}}}, ErrInvalidSort},
		{"bad sort order", TaskFilter{Sorts: []TaskSort{{Field: SortTitle, Order: SortOrder("sideways")}}}, ErrInvalidSort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			_, err := svc.ListTasks(context.Background(), tt.filter)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListTasks_Filters(t *testing.T) {
	svc, _ := newTestService(t)

	open, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "Купить молоко",
		Priority: Some(task.PriorityHigh),
		Tags:     []string{"дом", "покупки"},
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	completed, err := svc.CreateTask(context.Background(), CreateTaskInput{
		Title:    "Сдать отчёт",
		Priority: Some(task.PriorityLow),
		Tags:     []string{"работа"},
	}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CompleteTask(context.Background(), completed.Task.ID, 1, task.ActorUser); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	t.Run("status", func(t *testing.T) {
		result, err := svc.ListTasks(context.Background(), TaskFilter{Statuses: []task.Status{task.StatusCompleted}})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 || result.Tasks[0].Task.ID != completed.Task.ID {
			t.Errorf("got %d results", result.Total)
		}
	})

	t.Run("priority", func(t *testing.T) {
		result, err := svc.ListTasks(context.Background(), TaskFilter{Priorities: []task.Priority{task.PriorityHigh}})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 || result.Tasks[0].Task.ID != open.Task.ID {
			t.Errorf("got %d results", result.Total)
		}
	})

	t.Run("search title", func(t *testing.T) {
		result, err := svc.ListTasks(context.Background(), TaskFilter{Search: Some("молоко")})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 {
			t.Errorf("got %d results, want 1", result.Total)
		}
	})

	t.Run("search note", func(t *testing.T) {
		if _, err := svc.UpdateTask(context.Background(), open.Task.ID, 1, UpdateTaskInput{
			Note: Some("не забыть чек"),
		}, task.ActorUser); err != nil {
			t.Fatalf("UpdateTask failed: %v", err)
		}
		result, err := svc.ListTasks(context.Background(), TaskFilter{Search: Some("чек")})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 {
			t.Errorf("got %d results, want 1", result.Total)
		}
	})

	t.Run("tags AND logic", func(t *testing.T) {
		result, err := svc.ListTasks(context.Background(), TaskFilter{Tags: []string{"дом", "покупки"}})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 || result.Tasks[0].Task.ID != open.Task.ID {
			t.Errorf("got %d results, want only the task with both tags", result.Total)
		}

		none, err := svc.ListTasks(context.Background(), TaskFilter{Tags: []string{"дом", "работа"}})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if none.Total != 0 {
			t.Errorf("AND logic violated: got %d results", none.Total)
		}
	})

	t.Run("overdue", func(t *testing.T) {
		// open is overdue only if its deadline has passed; give it a past due date.
		current, err := svc.GetTask(context.Background(), open.Task.ID)
		if err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		past := task.CalendarDate{Year: 2026, Month: 1, Day: 1}
		if _, err := svc.UpdateTask(context.Background(), open.Task.ID, current.Version, UpdateTaskInput{
			DueDate: Some(past),
		}, task.ActorUser); err != nil {
			t.Fatalf("UpdateTask failed: %v", err)
		}

		result, err := svc.ListTasks(context.Background(), TaskFilter{Overdue: Some(true)})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if result.Total != 1 || result.Tasks[0].Task.ID != open.Task.ID {
			t.Errorf("overdue: got %d results", result.Total)
		}

		notOverdue, err := svc.ListTasks(context.Background(), TaskFilter{Overdue: Some(false)})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		for _, row := range notOverdue.Tasks {
			if row.Task.Status == task.StatusOpen && row.Task.ID != open.Task.ID {
				t.Errorf("task %d should be overdue", row.Task.ID)
			}
		}
	})
}

func TestListTasks_Sorting(t *testing.T) {
	svc, _ := newTestService(t)

	if _, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "b", Priority: Some(task.PriorityLow)}, task.ActorUser); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "a", Priority: Some(task.PriorityHigh)}, task.ActorUser); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	byTitle, err := svc.ListTasks(context.Background(), TaskFilter{
		Sorts: []TaskSort{{Field: SortTitle, Order: SortAsc}},
	})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if byTitle.Tasks[0].Task.Title != "a" || byTitle.Tasks[1].Task.Title != "b" {
		t.Errorf("title asc order = %q, %q", byTitle.Tasks[0].Task.Title, byTitle.Tasks[1].Task.Title)
	}

	byPriority, err := svc.ListTasks(context.Background(), TaskFilter{
		Sorts: []TaskSort{{Field: SortPriority, Order: SortAsc}},
	})
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if byPriority.Tasks[0].Task.Priority != task.PriorityHigh {
		t.Errorf("priority asc: first = %v, want high", byPriority.Tasks[0].Task.Priority)
	}
}

func TestParseSort(t *testing.T) {
	tests := []struct {
		raw   string
		field TaskSortField
		order SortOrder
		err   bool
	}{
		{"title", SortTitle, SortAsc, false},
		{"-title", SortTitle, SortDesc, false},
		{"+due_at", SortDueAt, SortAsc, false},
		{"-due_at", SortDueAt, SortDesc, false},
		{"", "", "", true},
		{"bogus", "", "", true},
		{"-bogus", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseSort(tt.raw)
			if tt.err {
				if !errors.Is(err, ErrInvalidSort) {
					t.Fatalf("error = %v, want ErrInvalidSort", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Field != tt.field || got.Order != tt.order {
				t.Errorf("got %+v, want field=%q order=%q", got, tt.field, tt.order)
			}
		})
	}
}
