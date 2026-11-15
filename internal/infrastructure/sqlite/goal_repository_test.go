package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// setupProductionDB opens the database exactly as the running application
// does, including the single-connection pool. The deadlock under test depends
// on that pool: a plain sql.Open with the default pool could hand the nested
// read a second connection and never reproduce the bug.
func setupProductionDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lifeos.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		t.Fatalf("Migrate failed: %v", err)
	}
	return db
}

// TestGoalProgressDoesNotDeadlockOnLinkedTask reproduces the review finding:
// the goal service used to call the task engine from inside an open goal
// transaction. SQLite runs with a single pooled connection, so the nested read
// waited for the connection the transaction already held and the call hung
// forever. The test fails on a timeout rather than on a value.
func TestGoalProgressDoesNotDeadlockOnLinkedTask(t *testing.T) {
	db := setupProductionDB(t)
	defer db.Close()

	// The regression is only meaningful under the production pool size; assert
	// it so a future change to the test setup cannot quietly disable the test.
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("pool size = %d, want 1: the deadlock only reproduces with one connection", got)
	}

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

// TestCompletionHookWritesGoalProgressSynchronously covers the primary path
// added after review: the journal is written before the completing call
// returns, so a lost event or a later task restore cannot erase the fact that
// the task was completed.
func TestCompletionHookWritesGoalProgressSynchronously(t *testing.T) {
	db := setupProductionDB(t)
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
	// No event pump is running here: only the synchronous hook can record.
	taskService.SetCompletionHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		goalService.OnTaskCompleted(ctx, completed.Task.ID)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := taskService.CreateTask(ctx, tasks.CreateTaskInput{Title: "Свести отчёт"}, "user")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	goal, err := goalService.Create(ctx, appgoals.CreateInput{Title: "Квартальный отчёт"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := goalService.AddLink(ctx, goal.Goal.ID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: created.Task.ID, Relation: "подготовка",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	completed, err := taskService.CompleteTask(ctx, created.Task.ID, created.Version, "user")
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	// The journal must already hold the entry, without waiting for a subscriber.
	view, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	countDone := func(v *appgoals.View) int {
		n := 0
		for _, event := range v.ProgressLog {
			if event.Kind == domaingoal.ProgressTaskDone {
				n++
			}
		}
		return n
	}
	if got := countDone(view); got != 1 {
		t.Fatalf("task_done entries right after completion = %d, want 1", got)
	}

	// Reopening the task does not erase the fact that it was completed: the
	// journal is history, while progress still follows the current status.
	if _, err := taskService.RestoreTask(ctx, created.Task.ID, completed.Version, "user"); err != nil {
		t.Fatalf("restore task: %v", err)
	}
	after, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal after restore: %v", err)
	}
	if got := countDone(after); got != 1 {
		t.Fatalf("task_done entries after restore = %d, want the history kept", got)
	}
	if after.Review.Progress.LinkedTasksDone != 0 {
		t.Fatalf("progress must follow the current task state, got %d done", after.Review.Progress.LinkedTasksDone)
	}
	// A reconciliation pass must not duplicate the history either.
	if added, err := goalService.ReconcileTaskProgress(ctx); err != nil || added != 0 {
		t.Fatalf("reconcile after restore added %d entries (err=%v), want 0", added, err)
	}
}

// TestReconcileRestoresProgressFromDurableCompletion covers the residual gap the
// review named: if the synchronous journal write fails and the task is reopened
// before the next pass, reconciliation can still rebuild the entry, because the
// completion is durable in the task history even though the task status no
// longer says "completed".
func TestReconcileRestoresProgressFromDurableCompletion(t *testing.T) {
	db := setupProductionDB(t)
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
	taskService.SetCompletionHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return goalService.OnTaskCompleted(ctx, completed.Task.ID)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := taskService.CreateTask(ctx, tasks.CreateTaskInput{Title: "Проверить смету"}, "user")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	goal, err := goalService.Create(ctx, appgoals.CreateInput{Title: "Запуск проекта"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := goalService.AddLink(ctx, goal.Goal.ID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: created.Task.ID, Relation: "смета",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	completed, err := taskService.CompleteTask(ctx, created.Task.ID, created.Version, "user")
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	// Simulate the failed write the review describes: the completion committed,
	// the journal entry did not land.
	if _, err := db.ExecContext(ctx, `DELETE FROM goal_progress WHERE goal_id = ? AND kind = 'task_done'`, goal.Goal.ID); err != nil {
		t.Fatalf("simulate failed journal write: %v", err)
	}
	// The task is then reopened before any reconciliation runs, so the current
	// status no longer proves the completion.
	if _, err := taskService.RestoreTask(ctx, created.Task.ID, completed.Version, "user"); err != nil {
		t.Fatalf("restore task: %v", err)
	}

	view, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if view.Review.Progress.LinkedTasksDone != 0 {
		t.Fatalf("task should be open again, got %d done", view.Review.Progress.LinkedTasksDone)
	}

	added, err := goalService.ReconcileTaskProgress(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if added != 1 {
		t.Fatalf("reconcile added %d entries, want 1 from the durable completion", added)
	}
	restored, err := goalService.Get(ctx, goal.Goal.ID)
	if err != nil {
		t.Fatalf("get goal after reconcile: %v", err)
	}
	found := false
	for _, entry := range restored.ProgressLog {
		if entry.Kind == domaingoal.ProgressTaskDone {
			found = true
		}
	}
	if !found {
		t.Fatalf("journal still has no task_done entry: %+v", restored.ProgressLog)
	}
	// Reconciliation stays idempotent: a second pass writes nothing.
	if again, err := goalService.ReconcileTaskProgress(ctx); err != nil || again != 0 {
		t.Fatalf("second reconcile added %d (err=%v), want 0", again, err)
	}
}
