// Package summary builds the aggregate shown on the Today screen.
//
// It is a read model: it composes the task and inbox services and derives
// overdue/today membership with the same domain rules the rest of the system
// uses, so the dashboard cannot disagree with the task list.
package summary

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// TaskLister is the slice of the task service the summary needs.
type TaskLister interface {
	ListTasks(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error)
}

// InboxCounter is the slice of the inbox service the summary needs.
type InboxCounter interface {
	CountUnprocessed(ctx context.Context) (int, error)
}

// TodayItem is one task scheduled for today.
type TodayItem struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Time     string `json:"time"`
	Priority string `json:"priority"`
}

// OverdueItem is one overdue task.
type OverdueItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Priority    string `json:"priority"`
	DaysOverdue int    `json:"days_overdue"`
}

// HighPriorityItem is one open high-priority task.
type HighPriorityItem struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// Stats are the counters shown on the dashboard.
type Stats struct {
	ActiveTotal       int `json:"active_total"`
	OverdueCount      int `json:"overdue_count"`
	HighPriorityCount int `json:"high_priority_count"`
	CompletedToday    int `json:"completed_today"`
	InboxCount        int `json:"inbox_count"`
}

// FocusTask is the task the dashboard suggests focusing on.
type FocusTask struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Time     string `json:"time"`
	Priority string `json:"priority"`
}

// Summary is the Today screen aggregate.
type Summary struct {
	Today        []TodayItem        `json:"today"`
	Overdue      []OverdueItem      `json:"overdue"`
	HighPriority []HighPriorityItem `json:"high_priority"`
	Stats        Stats              `json:"stats"`
	FocusTask    *FocusTask         `json:"focus_task"`
}

// Service builds summaries.
type Service struct {
	tasks    TaskLister
	inbox    InboxCounter
	clock    tasks.Clock
	timezone string
}

// NewService builds a summary service.
func NewService(taskLister TaskLister, inboxCounter InboxCounter, clock tasks.Clock, timezone string) *Service {
	if clock == nil {
		clock = tasks.SystemClock()
	}
	if timezone == "" {
		timezone = tasks.DefaultTimezone
	}
	return &Service{tasks: taskLister, inbox: inboxCounter, clock: clock, timezone: timezone}
}

// Today builds the aggregate for the current local day.
func (s *Service) Today(ctx context.Context) (Summary, error) {
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
		return Summary{}, err
	}
	completed, err := s.listAll(ctx, tasks.TaskFilter{Statuses: []task.Status{task.StatusCompleted}})
	if err != nil {
		return Summary{}, err
	}
	inboxCount, err := s.inbox.CountUnprocessed(ctx)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{
		Today:        []TodayItem{},
		Overdue:      []OverdueItem{},
		HighPriority: []HighPriorityItem{},
		Stats: Stats{
			ActiveTotal: len(open),
			InboxCount:  inboxCount,
		},
	}

	for _, item := range open {
		t := item.Task
		if t.IsOverdue(now) {
			summary.Overdue = append(summary.Overdue, OverdueItem{
				ID:          t.ID,
				Title:       t.Title,
				Priority:    string(t.Priority),
				DaysOverdue: daysOverdue(t, now),
			})
			summary.Stats.OverdueCount++
		}
		if t.Priority == task.PriorityHigh {
			summary.HighPriority = append(summary.HighPriority, HighPriorityItem{
				ID:       t.ID,
				Title:    t.Title,
				Priority: string(t.Priority),
			})
			summary.Stats.HighPriorityCount++
		}
		if isToday(t, dayStart, dayEnd) {
			summary.Today = append(summary.Today, TodayItem{
				ID:       t.ID,
				Title:    t.Title,
				Time:     taskTime(t, loc),
				Priority: string(t.Priority),
			})
		}
	}

	for _, item := range completed {
		if item.Task.CompletedAt != nil && !item.Task.CompletedAt.Before(dayStart) && item.Task.CompletedAt.Before(dayEnd) {
			summary.Stats.CompletedToday++
		}
	}

	sort.SliceStable(summary.Today, func(i, j int) bool {
		return summary.Today[i].Time < summary.Today[j].Time
	})
	sort.SliceStable(summary.Overdue, func(i, j int) bool {
		return summary.Overdue[i].DaysOverdue > summary.Overdue[j].DaysOverdue
	})

	summary.FocusTask = pickFocus(summary, open)
	return summary, nil
}

// listAll pages through a filter so the summary is not silently capped at one
// page of results.
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

// isToday reports whether a task belongs to the local day.
func isToday(t *task.Task, dayStart, dayEnd time.Time) bool {
	if t.PlannedDate != nil && !t.PlannedDate.IsZero() {
		planned := t.PlannedDate.Time(dayStart.Location())
		if planned.Equal(dayStart) {
			return true
		}
	}
	if t.DueDate != nil && !t.DueDate.IsZero() {
		due := t.DueDate.Time(dayStart.Location())
		if due.Equal(dayStart) {
			return true
		}
	}
	if t.DueAt != nil {
		dueAt := t.DueAt.In(dayStart.Location())
		if !dueAt.Before(dayStart) && dueAt.Before(dayEnd) {
			return true
		}
	}
	return false
}

// taskTime renders the local time of an exact deadline, or a dash.
func taskTime(t *task.Task, loc *time.Location) string {
	if t.DueAt == nil {
		return "—"
	}
	return t.DueAt.In(loc).Format("15:04")
}

// daysOverdue reports how many days a task has been overdue, at least one.
func daysOverdue(t *task.Task, now time.Time) int {
	boundary := t.OverdueAt()
	if boundary == nil {
		return 0
	}
	days := int(math.Ceil(now.Sub(*boundary).Hours() / 24))
	if days < 1 {
		days = 1
	}
	return days
}

// pickFocus chooses the task the dashboard should suggest: a high-priority task
// scheduled for today, otherwise the first task of today, otherwise the first
// open high-priority task.
func pickFocus(summary Summary, open []tasks.TaskWithVersion) *FocusTask {
	overdue := make(map[int64]bool, len(summary.Overdue))
	for _, item := range summary.Overdue {
		overdue[item.ID] = true
	}
	// Prefer a high-priority task that is still on time: suggesting an already
	// overdue task as "focus" hides the fact that it needs rescheduling first.
	for _, item := range summary.Today {
		if item.Priority == string(task.PriorityHigh) && !overdue[item.ID] {
			return &FocusTask{ID: item.ID, Title: item.Title, Time: item.Time, Priority: item.Priority}
		}
	}
	for _, item := range summary.Today {
		if item.Priority == string(task.PriorityHigh) {
			return &FocusTask{ID: item.ID, Title: item.Title, Time: item.Time, Priority: item.Priority}
		}
	}
	if len(summary.Today) > 0 {
		item := summary.Today[0]
		return &FocusTask{ID: item.ID, Title: item.Title, Time: item.Time, Priority: item.Priority}
	}
	for _, item := range open {
		if item.Task.Priority == task.PriorityHigh {
			return &FocusTask{
				ID:       item.Task.ID,
				Title:    item.Task.Title,
				Time:     "—",
				Priority: string(item.Task.Priority),
			}
		}
	}
	return nil
}
