package sqlite

import (
	"context"
	"testing"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// TestGoalProgressDoesNotDeadlockOnLinkedTask reproduces the review finding:
// the goal service used to call the task engine from inside an open goal
// transaction. SQLite here runs with a single pooled connection, so the nested
// read waited for the connection the transaction already held and the call hung
// forever. The test fails on a timeout rather than on a value.
func TestGoalProgressDoesNotDeadlockOnLinkedTask(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	clock := tasks.SystemClock()
	broker := events.NewBroker()
	taskService := tasks.NewService(NewTaskUnitOfWork(db), clock, tasks.WithPublisher(broker))
	goalService := appgoals.NewService(
		NewGoalUnitOfWork(db),
		clock,
		appgoals.WithTaskProvider(appgoals.TaskEngineAdapter{Service: taskService}),
		appgoals.WithPublisher(broker),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := taskService.CreateTask(ctx, tasks.CreateTaskInput{Title: "Подготовить данные"}, "user")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	goal, err := goalService.Create(ctx, appgoals.CreateInput{
		Title:      "Запустить аналитику",
		Milestones: []domaingoal.MilestoneInput{{Title: "Собрать данные"}},
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := goalService.AddLink(ctx, goal.Goal.ID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: created.Task.ID, Relation: "данные",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	// Reading a goal with a linked task must compute progress without hanging.
	view, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if view.Review == nil || view.Review.Progress.LinkedTasks != 1 {
		t.Fatalf("review = %+v, want one linked task", view.Review)
	}

	// Completing the task then reading again must also finish.
	if _, err := taskService.CompleteTask(ctx, created.Task.ID, created.Version, "user"); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	done, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal after completion: %v", err)
	}
	if done.Review.Progress.LinkedTasksDone != 1 {
		t.Fatalf("linked tasks done = %d, want 1", done.Review.Progress.LinkedTasksDone)
	}

	// Recording progress from the live pump path must not deadlock either.
	goalService.OnTaskCompleted(ctx, created.Task.ID)
}
