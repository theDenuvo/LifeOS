// Package calendar builds the calendar read model: events plus tasks for a
// date range, with conflicts between overlapping intervals.
package calendar

import (
	"context"
	"sort"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// TaskLister is the slice of the task service the calendar needs.
type TaskLister interface {
	ListTasks(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error)
}

// EventLister is the slice of the event service the calendar needs.
type EventLister interface {
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]appevents.EventWithVersion, error)
}

// Conflict describes overlapping calendar items.
type Conflict struct {
	Kind     string    `json:"kind"` // event_event | event_task
	EventIDs []int64   `json:"event_ids"`
	TaskIDs  []int64   `json:"task_ids,omitempty"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// Result is the calendar range read model.
type Result struct {
	From      task.CalendarDate            `json:"from"`
	To        task.CalendarDate            `json:"to"`
	Events    []appevents.EventWithVersion `json:"events"`
	Tasks     []tasks.TaskWithVersion      `json:"tasks"`
	Conflicts []Conflict                   `json:"conflicts"`
}

// Service composes events and tasks into one calendar view.
type Service struct {
	events   EventLister
	tasks    TaskLister
	timezone string
}

// NewService builds a calendar service.
func NewService(eventLister EventLister, taskLister TaskLister, timezone string) *Service {
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	return &Service{events: eventLister, tasks: taskLister, timezone: timezone}
}

// Range returns events and tasks for the inclusive local date range.
func (s *Service) Range(ctx context.Context, from, to task.CalendarDate, calendars []event.Calendar) (Result, error) {
	if from.IsZero() || to.IsZero() || from.String() > to.String() {
		return Result{}, appevents.ErrInvalidDateRange
	}
	loc, err := time.LoadLocation(s.timezone)
	if err != nil {
		loc = time.UTC
	}

	rangeStart := from.Time(loc)
	rangeEnd := to.Time(loc).AddDate(0, 0, 1)

	events, err := s.events.ListRange(ctx, rangeStart, rangeEnd, calendars)
	if err != nil {
		return Result{}, err
	}

	allTasks, err := s.listAllTasks(ctx)
	if err != nil {
		return Result{}, err
	}

	filteredTasks := make([]tasks.TaskWithVersion, 0, len(allTasks))
	for _, item := range allTasks {
		date, ok := effectiveTaskDate(item.Task, loc)
		if !ok {
			continue
		}
		if date.String() >= from.String() && date.String() <= to.String() {
			filteredTasks = append(filteredTasks, item)
		}
	}

	return Result{
		From:      from,
		To:        to,
		Events:    events,
		Tasks:     filteredTasks,
		Conflicts: conflicts(events, filteredTasks),
	}, nil
}

func (s *Service) listAllTasks(ctx context.Context) ([]tasks.TaskWithVersion, error) {
	const pageSize = tasks.MaxPageLimit
	all := make([]tasks.TaskWithVersion, 0, pageSize)
	offset := 0
	for {
		page, err := s.tasks.ListTasks(ctx, tasks.TaskFilter{
			Limit:  tasks.Some(pageSize),
			Offset: tasks.Some(offset),
		})
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

// effectiveTaskDate is the date a task appears on in the calendar. An explicit
// due date wins over a planned date; an exact deadline is mapped to the local
// calendar day.
func effectiveTaskDate(value *task.Task, loc *time.Location) (task.CalendarDate, bool) {
	if value.DueDate != nil && !value.DueDate.IsZero() {
		return *value.DueDate, true
	}
	if value.PlannedDate != nil && !value.PlannedDate.IsZero() {
		return *value.PlannedDate, true
	}
	if value.DueAt != nil {
		local := value.DueAt.In(loc)
		date, err := task.ParseCalendarDate(local.Format("2006-01-02"))
		if err != nil {
			return task.CalendarDate{}, false
		}
		return date, true
	}
	return task.CalendarDate{}, false
}

func conflicts(events []appevents.EventWithVersion, tasks []tasks.TaskWithVersion) []Conflict {
	result := make([]Conflict, 0)

	for i := 0; i < len(events); i++ {
		left := events[i].Event
		for j := i + 1; j < len(events); j++ {
			right := events[j].Event
			if overlaps(left.StartsAt, left.EndsAt, right.StartsAt, right.EndsAt) {
				result = append(result, Conflict{
					Kind:     "event_event",
					EventIDs: []int64{left.ID, right.ID},
					StartsAt: maxTime(left.StartsAt, right.StartsAt),
					EndsAt:   minTime(left.EndsAt, right.EndsAt),
				})
			}
		}
	}

	for _, item := range events {
		value := item.Event
		for _, taskItem := range tasks {
			dueAt := taskItem.Task.DueAt
			if dueAt == nil || taskItem.Task.Status != task.StatusOpen {
				continue
			}
			if !dueAt.Before(value.StartsAt) && dueAt.Before(value.EndsAt) {
				result = append(result, Conflict{
					Kind:     "event_task",
					EventIDs: []int64{value.ID},
					TaskIDs:  []int64{taskItem.Task.ID},
					StartsAt: maxTime(value.StartsAt, *dueAt),
					EndsAt:   minTime(value.EndsAt, dueAt.Add(time.Minute)),
				})
			}
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].StartsAt.Equal(result[j].StartsAt) {
			return result[i].Kind < result[j].Kind
		}
		return result[i].StartsAt.Before(result[j].StartsAt)
	})
	return result
}

func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
