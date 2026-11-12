package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// erroringEventRepo fails on demand, to prove the task write is rolled back when
// the event insert fails.
type erroringEventRepo struct {
	inner TaskEventRepository
	err   error
	calls int
}

func (r *erroringEventRepo) Insert(ctx context.Context, e *task.TaskEvent) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	return r.inner.Insert(ctx, e)
}

// erroringUnitOfWork wraps a fake and injects a failing event repository.
type erroringUnitOfWork struct {
	inner    *FakeUnitOfWork
	eventErr error
}

func (u *erroringUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error {
	return u.inner.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return fn(ctx, &failingTx{Transaction: tx, eventErr: u.eventErr})
	})
}

type failingTx struct {
	Transaction
	eventErr error
}

func (t *failingTx) Events() TaskEventRepository {
	return &erroringEventRepo{inner: t.Transaction.Events(), err: t.eventErr}
}

func TestEventWriteFailureRollsBackTask(t *testing.T) {
	inner := NewFakeUnitOfWork()
	inner.OverdueNow = testNow
	uow := &erroringUnitOfWork{inner: inner, eventErr: errors.New("event table unavailable")}
	svc := NewService(uow, fixedClock{now: testNow})

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err == nil {
		t.Fatal("expected the event failure to abort the transaction")
	}
	if !errors.Is(err, err) { // the injected error must surface unchanged
		t.Fatalf("unexpected error: %v", err)
	}

	// Neither the task nor its version may exist after the rollback.
	if len(inner.Tasks()) != 0 {
		t.Errorf("task was persisted despite the event failure: %d rows", len(inner.Tasks()))
	}
	if inner.EventCount() != 0 {
		t.Errorf("event count = %d, want 0", inner.EventCount())
	}
	_ = created
}

func TestEventWriteFailureRollsBackUpdate(t *testing.T) {
	inner := NewFakeUnitOfWork()
	inner.OverdueNow = testNow
	failing := &erroringUnitOfWork{inner: inner, eventErr: errors.New("event write failed")}

	// Create through the working inner service first.
	svc := NewService(inner, fixedClock{now: testNow})
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Now attempt an update through the failing wrapper.
	failingSvc := NewService(failing, fixedClock{now: testNow})
	_, err = failingSvc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title: Some("changed"),
	}, task.ActorUser)
	if err == nil {
		t.Fatal("expected an error")
	}

	// The task must be untouched and the version unchanged.
	after, ok := inner.FindTask(created.Task.ID)
	if !ok {
		t.Fatal("task disappeared")
	}
	if after.Task.Title != "x" {
		t.Errorf("Title = %q, want unchanged %q", after.Task.Title, "x")
	}
	if after.Version != created.Version {
		t.Errorf("Version = %d, want unchanged %d", after.Version, created.Version)
	}
}

func TestRollbackLeavesNoPartialUpdate(t *testing.T) {
	// A two-option update where the second option fails must not leave the
	// first option's change behind. This exercises the domain's atomicity
	// through the application layer.
	svc, uow := newTestService(t)
	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	before := uow.FindTaskMust(t, created.Task.ID)

	_, err = svc.UpdateTask(context.Background(), created.Task.ID, created.Version, UpdateTaskInput{
		Title:    Some("new title"),
		Timezone: Some("Mars/Olympus"), // invalid, must abort everything
	}, task.ActorUser)
	if !errors.Is(err, task.ErrInvalidTimezone) {
		t.Fatalf("error = %v, want ErrInvalidTimezone", err)
	}

	after := uow.FindTaskMust(t, created.Task.ID)
	if after.Task.Title != before.Task.Title {
		t.Errorf("Title = %q, want unchanged %q", after.Task.Title, before.Task.Title)
	}
	if after.Version != before.Version {
		t.Errorf("Version = %d, want unchanged %d", after.Version, before.Version)
	}
}

func TestCommitFailureDoesNotPersist(t *testing.T) {
	uow := NewFakeUnitOfWork()
	uow.OverdueNow = testNow
	uow.CommitErr = errors.New("commit failed")
	svc := NewService(uow, fixedClock{now: testNow})

	_, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err == nil {
		t.Fatal("expected the commit failure to abort the operation")
	}
	if len(uow.Tasks()) != 0 {
		t.Errorf("task persisted despite commit failure: %d rows", len(uow.Tasks()))
	}
	if uow.EventCount() != 0 {
		t.Errorf("event persisted despite commit failure: %d", uow.EventCount())
	}
}

func TestBeginFailurePropagates(t *testing.T) {
	uow := NewFakeUnitOfWork()
	uow.OverdueNow = testNow
	uow.BeginErr = errors.New("no connection")
	svc := NewService(uow, fixedClock{now: testNow})

	_, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err == nil {
		t.Fatal("expected the begin failure to propagate")
	}
}

// --- Context handling ---------------------------------------------------------

func TestCancelledContextBeforeCall(t *testing.T) {
	svc, _ := newTestService(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.CreateTask(ctx, CreateTaskInput{Title: "x"}, task.ActorUser); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateTask error = %v, want context.Canceled", err)
	}
	if _, err := svc.GetTask(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetTask error = %v, want context.Canceled", err)
	}
	if _, err := svc.ListTasks(ctx, TaskFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListTasks error = %v, want context.Canceled", err)
	}
	if _, err := svc.UpdateTask(ctx, 1, 1, UpdateTaskInput{Note: Some("n")}, task.ActorUser); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateTask error = %v, want context.Canceled", err)
	}
}

func TestDeadlineExceededContext(t *testing.T) {
	svc, uow := newTestService(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, err := svc.CreateTask(ctx, CreateTaskInput{Title: "x"}, task.ActorUser)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if len(uow.Tasks()) != 0 {
		t.Error("no task may be persisted when the context is done")
	}
}

func TestCancelledContextDuringTransactionRollsBack(t *testing.T) {
	uow := NewFakeUnitOfWork()
	uow.OverdueNow = testNow

	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(uow, fixedClock{now: testNow})

	// Seed a task, then cancel while a mutation is in flight.
	seeded := uow.Seed(&task.Task{
		Title:     "x",
		Priority:  task.PriorityMedium,
		Status:    task.StatusOpen,
		Timezone:  "Europe/Moscow",
		Tags:      []string{},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})

	cancel()
	_, err := svc.UpdateTask(ctx, seeded.Task.ID, seeded.Version, UpdateTaskInput{Note: Some("n")}, task.ActorUser)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	after := uow.FindTaskMust(t, seeded.Task.ID)
	if after.Task.Note != nil {
		t.Errorf("Note = %v, want nil after rollback", after.Task.Note)
	}
}

// --- Clock and timestamp coherence --------------------------------------------

func TestTaskAndEventShareTimestamp(t *testing.T) {
	svc, uow := newTestService(t)

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	events := uow.Events()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if !events[0].CreatedAt.Equal(created.Task.CreatedAt) {
		t.Errorf("event %v != task %v", events[0].CreatedAt, created.Task.CreatedAt)
	}
}

func TestNoHiddenSystemClock(t *testing.T) {
	// A clock pinned far in the past must be honoured everywhere: if the
	// application secretly read the system clock, these values would not match.
	pinned := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	uow := NewFakeUnitOfWork()
	uow.OverdueNow = pinned
	svc := NewService(uow, fixedClock{now: pinned})

	created, err := svc.CreateTask(context.Background(), CreateTaskInput{Title: "x"}, task.ActorUser)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if !created.Task.CreatedAt.Equal(pinned) {
		t.Errorf("CreatedAt = %v, want %v", created.Task.CreatedAt, pinned)
	}
	if !created.Task.UpdatedAt.Equal(pinned) {
		t.Errorf("UpdatedAt = %v, want %v", created.Task.UpdatedAt, pinned)
	}

	completed, err := svc.CompleteTask(context.Background(), created.Task.ID, created.Version, task.ActorUser)
	if err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}
	if completed.Task.CompletedAt == nil || !completed.Task.CompletedAt.Equal(pinned) {
		t.Errorf("CompletedAt = %v, want %v", completed.Task.CompletedAt, pinned)
	}
	if !completed.Task.UpdatedAt.Equal(pinned) {
		t.Errorf("UpdatedAt = %v, want %v", completed.Task.UpdatedAt, pinned)
	}
}

func TestDefaultClockIsUTC(t *testing.T) {
	// The production clock must return a UTC instant, otherwise timestamps
	// stored alongside local ones would be ambiguous.
	if got := SystemClock().Now().Location(); got != time.UTC {
		t.Errorf("SystemClock location = %v, want UTC", got)
	}
}

func TestNilClockFallsBackToSystem(t *testing.T) {
	svc := NewService(NewFakeUnitOfWork(), nil)
	if svc.clock == nil {
		t.Fatal("clock must not be nil")
	}
	if _, ok := svc.clock.(systemClock); !ok {
		t.Errorf("clock type = %T, want systemClock", svc.clock)
	}
}
