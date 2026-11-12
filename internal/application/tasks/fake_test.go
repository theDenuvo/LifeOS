package tasks

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// fakeData is the in-memory store shared by a fake Unit of Work.
//
// Writes go to a staging area first and only become visible on commit, so a
// rollback genuinely discards them — this is what makes the rollback tests
// meaningful rather than a stub returning canned values.
type fakeData struct {
	mu     sync.Mutex
	tasks  map[int64]storedTask
	events []*task.TaskEvent
	nextID int64
}

type storedTask struct {
	task    *task.Task
	version int
}

func newFakeData() *fakeData {
	return &fakeData{
		tasks:  make(map[int64]storedTask),
		nextID: 1,
	}
}

// FakeUnitOfWork is an in-memory UnitOfWork for tests.
type FakeUnitOfWork struct {
	data *fakeData

	// CommitErr, when set, makes Commit fail after fn succeeded.
	CommitErr error

	// BeginErr, when set, makes Begin fail.
	BeginErr error

	// OverdueNow is the instant used to evaluate the `overdue` filter.
	// It is never read from the system clock; tests set it explicitly.
	OverdueNow time.Time
}

// NewFakeUnitOfWork returns an empty fake unit of work.
func NewFakeUnitOfWork() *FakeUnitOfWork {
	return &FakeUnitOfWork{data: newFakeData()}
}

// Seed inserts a task directly, bypassing the service. Version starts at 1.
func (u *FakeUnitOfWork) Seed(t *task.Task) TaskWithVersion {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	id := u.data.nextID
	u.data.nextID++
	stored := cloneTask(t)
	stored.ID = id
	u.data.tasks[id] = storedTask{task: stored, version: 1}
	return TaskWithVersion{Task: cloneTask(stored), Version: 1}
}

// SeedWithVersion inserts a task at a specific version, for conflict tests.
func (u *FakeUnitOfWork) SeedWithVersion(t *task.Task, version int) TaskWithVersion {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	id := u.data.nextID
	u.data.nextID++
	stored := cloneTask(t)
	stored.ID = id
	u.data.tasks[id] = storedTask{task: stored, version: version}
	return TaskWithVersion{Task: cloneTask(stored), Version: version}
}

// Tasks returns a snapshot of all stored tasks.
func (u *FakeUnitOfWork) Tasks() []TaskWithVersion {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	ids := make([]int64, 0, len(u.data.tasks))
	for id := range u.data.tasks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]TaskWithVersion, 0, len(ids))
	for _, id := range ids {
		stored := u.data.tasks[id]
		out = append(out, TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version})
	}
	return out
}

// Events returns a copy of the appended events.
func (u *FakeUnitOfWork) Events() []*task.TaskEvent {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	out := make([]*task.TaskEvent, len(u.data.events))
	copy(out, u.data.events)
	return out
}

// EventCount reports how many events were committed.
func (u *FakeUnitOfWork) EventCount() int {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()
	return len(u.data.events)
}

// FindTask returns a stored task by ID.
func (u *FakeUnitOfWork) FindTask(id int64) (TaskWithVersion, bool) {
	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	stored, ok := u.data.tasks[id]
	if !ok {
		return TaskWithVersion{}, false
	}
	return TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version}, true
}

// Do implements UnitOfWork with staged writes and real rollback.
func (u *FakeUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if u.BeginErr != nil {
		return u.BeginErr
	}

	tx := &fakeTransaction{
		uow:    u,
		staged: make(map[int64]storedTask),
		events: make([]*task.TaskEvent, 0, 4),
	}

	if err := fn(ctx, tx); err != nil {
		// Rollback: staged writes and staged events are simply dropped.
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if u.CommitErr != nil {
		return u.CommitErr
	}

	u.data.mu.Lock()
	defer u.data.mu.Unlock()

	for id, stored := range tx.staged {
		u.data.tasks[id] = stored
	}
	u.data.events = append(u.data.events, tx.events...)
	return nil
}

// fakeTransaction stages writes until the unit of work commits.
type fakeTransaction struct {
	uow    *FakeUnitOfWork
	staged map[int64]storedTask
	events []*task.TaskEvent
}

func (t *fakeTransaction) Tasks() TaskRepository { return &fakeTaskRepo{tx: t} }

func (t *fakeTransaction) Events() TaskEventRepository { return &fakeEventRepo{tx: t} }

// fakeTaskRepo reads through to the committed data and writes into the staging
// area, so reads inside a transaction observe earlier writes in the same
// transaction but a rollback discards them.
type fakeTaskRepo struct {
	tx *fakeTransaction
}

func (r *fakeTaskRepo) lookup(id int64) (storedTask, bool) {
	if stored, ok := r.tx.staged[id]; ok {
		return stored, true
	}
	r.tx.uow.data.mu.Lock()
	defer r.tx.uow.data.mu.Unlock()
	stored, ok := r.tx.uow.data.tasks[id]
	if !ok {
		return storedTask{}, false
	}
	return storedTask{task: cloneTask(stored.task), version: stored.version}, true
}

func (r *fakeTaskRepo) FindByID(ctx context.Context, id int64) (TaskWithVersion, error) {
	if err := ctx.Err(); err != nil {
		return TaskWithVersion{}, err
	}
	stored, ok := r.lookup(id)
	if !ok {
		return TaskWithVersion{}, ErrTaskNotFound
	}
	return TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version}, nil
}

func (r *fakeTaskRepo) Insert(ctx context.Context, t *task.Task) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	r.tx.uow.data.mu.Lock()
	id := r.tx.uow.data.nextID
	r.tx.uow.data.nextID++
	r.tx.uow.data.mu.Unlock()

	stored := cloneTask(t)
	stored.ID = id
	r.tx.staged[id] = storedTask{task: stored, version: 1}
	return id, nil
}

func (r *fakeTaskRepo) Update(ctx context.Context, id int64, expectedVersion int, t *task.Task) (TaskWithVersion, error) {
	if err := ctx.Err(); err != nil {
		return TaskWithVersion{}, err
	}

	stored, ok := r.lookup(id)
	if !ok {
		return TaskWithVersion{}, ErrTaskNotFound
	}
	if stored.version != expectedVersion {
		return TaskWithVersion{}, ErrVersionConflict
	}

	updated := cloneTask(t)
	updated.ID = id
	next := stored.version + 1
	r.tx.staged[id] = storedTask{task: updated, version: next}
	return TaskWithVersion{Task: cloneTask(updated), Version: next}, nil
}

func (r *fakeTaskRepo) List(ctx context.Context, filter TaskFilter) (ListTasksResult, error) {
	if err := ctx.Err(); err != nil {
		return ListTasksResult{}, err
	}

	// Snapshot committed plus staged rows.
	r.tx.uow.data.mu.Lock()
	rows := make([]TaskWithVersion, 0, len(r.tx.uow.data.tasks))
	for id, stored := range r.tx.uow.data.tasks {
		rows = append(rows, TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version})
		_ = id
	}
	r.tx.uow.data.mu.Unlock()

	for id, stored := range r.tx.staged {
		replaced := false
		for i := range rows {
			if rows[i].Task.ID == id {
				rows[i] = TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version}
				replaced = true
				break
			}
		}
		if !replaced {
			rows = append(rows, TaskWithVersion{Task: cloneTask(stored.task), Version: stored.version})
		}
	}

	filtered := make([]TaskWithVersion, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.Task, filter, r.tx.uow) {
			filtered = append(filtered, row)
		}
	}

	applySort(filtered, filter.Sorts)

	// A repository always returns rows in a deterministic order. When the
	// caller supplies no sort, fall back to insertion order (by ID) so paging
	// is stable rather than dependent on map iteration.
	if len(filter.Sorts) == 0 {
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Task.ID < filtered[j].Task.ID
		})
	}

	total := len(filtered)
	limit := DefaultPageLimit
	if value, ok := filter.Limit.Get(); ok {
		limit = value
	}
	offset := 0
	if value, ok := filter.Offset.Get(); ok {
		offset = value
	}

	start := offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return ListTasksResult{
		Tasks:  filtered[start:end],
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// fakeEventRepo stages events; they become visible only on commit.
type fakeEventRepo struct {
	tx *fakeTransaction
}

func (r *fakeEventRepo) Insert(ctx context.Context, e *task.TaskEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copied := *e
	r.tx.events = append(r.tx.events, &copied)
	return nil
}

// matchesFilter implements the filter semantics the application promises.
func matchesFilter(t *task.Task, filter TaskFilter, uow *FakeUnitOfWork) bool {
	if len(filter.Statuses) > 0 {
		found := false
		for _, status := range filter.Statuses {
			if t.Status == status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.Priorities) > 0 {
		found := false
		for _, priority := range filter.Priorities {
			if t.Priority == priority {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if value, ok := filter.PlannedDate.Get(); ok {
		if t.PlannedDate == nil || *t.PlannedDate != value {
			return false
		}
	}

	if value, ok := filter.DueDate.Get(); ok {
		if t.DueDate == nil || *t.DueDate != value {
			return false
		}
	}

	if value, ok := filter.Overdue.Get(); ok {
		// The fake has no clock; overdue filtering is delegated through a
		// package-level reference instant set by the test.
		overdue := t.IsOverdue(uow.OverdueNow)
		if overdue != value {
			return false
		}
	}

	if value, ok := filter.Search.Get(); ok {
		needle := strings.ToLower(value)
		if needle != "" {
			title := strings.ToLower(t.Title)
			note := ""
			if t.Note != nil {
				note = strings.ToLower(*t.Note)
			}
			if !strings.Contains(title, needle) && !strings.Contains(note, needle) {
				return false
			}
		}
	}

	// Tags use AND logic: every requested tag must be present.
	if len(filter.Tags) > 0 {
		for _, wanted := range filter.Tags {
			present := false
			for _, tag := range t.Tags {
				if tag == wanted {
					present = true
					break
				}
			}
			if !present {
				return false
			}
		}
	}

	return true
}

func applySort(rows []TaskWithVersion, sorts []TaskSort) {
	if len(sorts) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, s := range sorts {
			less := compareRows(rows[i].Task, rows[j].Task, s.Field)
			if !less {
				continue
			}
			if s.Order == SortDesc {
				return false
			}
			return true
		}
		return false
	})
}

func compareRows(a, b *task.Task, field TaskSortField) bool {
	switch field {
	case SortTitle:
		return a.Title < b.Title
	case SortPriority:
		return priorityRank(a.Priority) < priorityRank(b.Priority)
	case SortCreatedAt:
		return a.CreatedAt.Before(b.CreatedAt)
	case SortUpdatedAt:
		return a.UpdatedAt.Before(b.UpdatedAt)
	case SortDueAt:
		return dueAtFor(a).Before(dueAtFor(b))
	default:
		return false
	}
}

func priorityRank(p task.Priority) int {
	switch p {
	case task.PriorityHigh:
		return 0
	case task.PriorityMedium:
		return 1
	default:
		return 2
	}
}

func dueAtFor(t *task.Task) time.Time {
	if t.DueAt != nil {
		return *t.DueAt
	}
	return time.Time{}
}

// cloneTask deep-copies a task so the fake never hands out shared state.
func cloneTask(t *task.Task) *task.Task {
	if t == nil {
		return nil
	}
	copied := *t
	if t.Note != nil {
		note := *t.Note
		copied.Note = &note
	}
	if t.DueAt != nil {
		dueAt := *t.DueAt
		copied.DueAt = &dueAt
	}
	if t.CompletedAt != nil {
		v := *t.CompletedAt
		copied.CompletedAt = &v
	}
	if t.CancelledAt != nil {
		v := *t.CancelledAt
		copied.CancelledAt = &v
	}
	if t.PlannedDate != nil {
		v := *t.PlannedDate
		copied.PlannedDate = &v
	}
	if t.DueDate != nil {
		v := *t.DueDate
		copied.DueDate = &v
	}
	if t.Tags != nil {
		tags := make([]string, len(t.Tags))
		copy(tags, t.Tags)
		copied.Tags = tags
	}
	return &copied
}

// FindTaskMust returns a stored task or fails the test.
func (u *FakeUnitOfWork) FindTaskMust(t *testing.T, id int64) TaskWithVersion {
	t.Helper()
	found, ok := u.FindTask(id)
	if !ok {
		t.Fatalf("task %d not found", id)
	}
	return found
}
