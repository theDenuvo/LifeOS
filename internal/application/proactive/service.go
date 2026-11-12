// Package proactive derives useful observations from the user's data.
//
// Insights are computed, not stored: the database only remembers which ones the
// user dismissed or snoozed. That keeps the detectors honest — an insight
// disappears as soon as the underlying situation is fixed.
package proactive

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Severity orders insights by urgency.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Insight is one derived observation.
type Insight struct {
	Key      string   `json:"key"`
	Kind     string   `json:"kind"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Count    int      `json:"count"`
	TaskIDs  []int64  `json:"task_ids"`
}

// State is the stored user decision for one insight key.
type State struct {
	Key          string
	DismissedAt  *time.Time
	SnoozedUntil *time.Time
}

// Repository persists insight state.
type Repository interface {
	ListStates(ctx context.Context, keys []string) (map[string]State, error)
	Dismiss(ctx context.Context, key string, at time.Time) error
	Snooze(ctx context.Context, key string, until, at time.Time) error
}

// Transaction exposes the insight repository bound to one transaction.
type Transaction interface {
	Insights() Repository
}

// UnitOfWork defines the transaction boundary.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// TaskLister is the slice of the task service the detectors need.
type TaskLister interface {
	ListTasks(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error)
}

// InboxCounter is the slice of the inbox service the detectors need.
type InboxCounter interface {
	CountUnprocessed(ctx context.Context) (int, error)
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}

// Service computes and manages insights.
type Service struct {
	uow      UnitOfWork
	tasks    TaskLister
	inbox    InboxCounter
	clock    Clock
	timezone string
}

// NewService builds the proactive service.
func NewService(uow UnitOfWork, taskLister TaskLister, inboxCounter InboxCounter, clock Clock, timezone string) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	if timezone == "" {
		timezone = tasks.DefaultTimezone
	}
	return &Service{uow: uow, tasks: taskLister, inbox: inboxCounter, clock: clock, timezone: timezone}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Compute returns the currently relevant insights, excluding dismissed and
// snoozed ones.
func (s *Service) Compute(ctx context.Context) ([]Insight, error) {
	now := s.clock.Now()
	loc, err := time.LoadLocation(s.timezone)
	if err != nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	open, err := s.listAll(ctx, tasks.TaskFilter{Statuses: []task.Status{task.StatusOpen}})
	if err != nil {
		return nil, err
	}
	inboxCount, err := s.inbox.CountUnprocessed(ctx)
	if err != nil {
		return nil, err
	}

	insights := s.detect(open, inboxCount, now, dayStart, dayEnd)
	if len(insights) == 0 {
		return []Insight{}, nil
	}

	keys := make([]string, 0, len(insights))
	for _, insight := range insights {
		keys = append(keys, insight.Key)
	}

	states := map[string]State{}
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Insights().ListStates(ctx, keys)
		if err != nil {
			return err
		}
		states = found
		return nil
	})
	if err != nil {
		return nil, err
	}

	visible := make([]Insight, 0, len(insights))
	for _, insight := range insights {
		state, ok := states[insight.Key]
		if ok {
			if state.DismissedAt != nil {
				continue
			}
			if state.SnoozedUntil != nil && state.SnoozedUntil.After(now) {
				continue
			}
		}
		visible = append(visible, insight)
	}

	sort.SliceStable(visible, func(i, j int) bool {
		return severityRank(visible[i].Severity) < severityRank(visible[j].Severity)
	})
	return visible, nil
}

// Dismiss hides an insight until the user clears the state.
func (s *Service) Dismiss(ctx context.Context, key string) error {
	now := s.clock.Now()
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Insights().Dismiss(ctx, key, now)
	})
}

// Snooze hides an insight until the given instant.
func (s *Service) Snooze(ctx context.Context, key string, until time.Time) error {
	now := s.clock.Now()
	if until.IsZero() {
		until = now.Add(24 * time.Hour)
	}
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Insights().Snooze(ctx, key, until, now)
	})
}

// detect runs every detector over the current data.
func (s *Service) detect(open []tasks.TaskWithVersion, inboxCount int, now, dayStart, dayEnd time.Time) []Insight {
	insights := make([]Insight, 0, 6)

	overdue := make([]int64, 0)
	for _, item := range open {
		if item.Task.IsOverdue(now) {
			overdue = append(overdue, item.Task.ID)
		}
	}
	if len(overdue) > 0 {
		insights = append(insights, Insight{
			Key:      "overdue",
			Kind:     "overdue",
			Severity: SeverityCritical,
			Title:    "Просроченные задачи",
			Detail:   fmt.Sprintf("%d задач(и) просрочены — стоит перенести или закрыть", len(overdue)),
			Count:    len(overdue),
			TaskIDs:  limitIDs(overdue, 10),
		})
	}

	today := make([]tasks.TaskWithVersion, 0)
	highToday := 0
	for _, item := range open {
		if item.Task.IsOverdue(now) {
			continue
		}
		if isToday(item.Task, dayStart, dayEnd) {
			today = append(today, item)
			if item.Task.Priority == task.PriorityHigh {
				highToday++
			}
		}
	}
	if len(today) > 0 {
		ids := make([]int64, 0, len(today))
		for _, item := range today {
			ids = append(ids, item.Task.ID)
		}
		insights = append(insights, Insight{
			Key:      "due-today",
			Kind:     "due-today",
			Severity: SeverityWarning,
			Title:    "Дедлайны сегодня",
			Detail:   fmt.Sprintf("На сегодня запланировано %d задач(и)", len(today)),
			Count:    len(today),
			TaskIDs:  limitIDs(ids, 10),
		})
	}

	if len(today) >= 8 || highToday >= 4 {
		insights = append(insights, Insight{
			Key:      "overload",
			Kind:     "overload",
			Severity: SeverityWarning,
			Title:    "Перегруженный день",
			Detail:   fmt.Sprintf("%d задач на сегодня, из них %d высокого приоритета", len(today), highToday),
			Count:    len(today),
		})
	}

	if inboxCount > 10 {
		insights = append(insights, Insight{
			Key:      "inbox-backlog",
			Kind:     "inbox-backlog",
			Severity: SeverityInfo,
			Title:    "Входящие накопились",
			Detail:   fmt.Sprintf("%d элементов ждут разбора", inboxCount),
			Count:    inboxCount,
		})
	}

	stale := make([]int64, 0)
	staleBefore := now.AddDate(0, 0, -14)
	for _, item := range open {
		t := item.Task
		if t.DueAt == nil && t.DueDate == nil && t.CreatedAt.Before(staleBefore) {
			stale = append(stale, t.ID)
		}
	}
	if len(stale) > 0 {
		insights = append(insights, Insight{
			Key:      "stale",
			Kind:     "stale",
			Severity: SeverityInfo,
			Title:    "Задачи без срока",
			Detail:   fmt.Sprintf("%d задач(и) висят без дедлайна больше двух недель", len(stale)),
			Count:    len(stale),
			TaskIDs:  limitIDs(stale, 10),
		})
	}

	// Two open tasks with the exact same deadline minute are likely a conflict
	// the user should see before the day starts.
	byMinute := make(map[string][]int64)
	for _, item := range open {
		if item.Task.DueAt == nil {
			continue
		}
		key := item.Task.DueAt.UTC().Truncate(time.Minute).Format(time.RFC3339)
		byMinute[key] = append(byMinute[key], item.Task.ID)
	}
	conflicts := make([]int64, 0)
	for _, ids := range byMinute {
		if len(ids) > 1 {
			conflicts = append(conflicts, ids...)
		}
	}
	if len(conflicts) > 0 {
		insights = append(insights, Insight{
			Key:      "duplicate-deadline",
			Kind:     "duplicate-deadline",
			Severity: SeverityWarning,
			Title:    "Несколько дедлайнов в одно время",
			Detail:   fmt.Sprintf("%d задач(и) имеют совпадающее точное время", len(conflicts)),
			Count:    len(conflicts),
			TaskIDs:  limitIDs(conflicts, 10),
		})
	}

	return insights
}

func (s *Service) listAll(ctx context.Context, filter tasks.TaskFilter) ([]tasks.TaskWithVersion, error) {
	const pageSize = tasks.MaxPageLimit
	all := make([]tasks.TaskWithVersion, 0, pageSize)
	offset := 0
	for {
		filter.Limit = tasks.Some(pageSize)
		filter.Offset = tasks.Some(offset)
		page, err := s.tasks.ListTasks(ctx, filter)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Tasks...)
		offset += len(page.Tasks)
		if len(page.Tasks) == 0 || offset >= page.Total {
			return all, nil
		}
	}
}

func isToday(t *task.Task, dayStart, dayEnd time.Time) bool {
	if t.PlannedDate != nil && !t.PlannedDate.IsZero() && t.PlannedDate.Time(dayStart.Location()).Equal(dayStart) {
		return true
	}
	if t.DueDate != nil && !t.DueDate.IsZero() && t.DueDate.Time(dayStart.Location()).Equal(dayStart) {
		return true
	}
	if t.DueAt != nil {
		dueAt := t.DueAt.In(dayStart.Location())
		return !dueAt.Before(dayStart) && dueAt.Before(dayEnd)
	}
	return false
}

func limitIDs(ids []int64, limit int) []int64 {
	if len(ids) <= limit {
		return ids
	}
	return ids[:limit]
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}
