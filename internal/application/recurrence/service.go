// Package recurrence turns a completed recurring task into its next occurrence.
//
// A recurring task is a template: the completed instance stays in the history and
// a fresh copy is created for the next date. That way "did I practise on Monday"
// is answerable from the task list itself, without a separate habit log.
package recurrence

import (
	"context"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Service creates the next occurrence of a recurring task.
type Service struct {
	tasks    *tasks.Service
	clock    tasks.Clock
	timezone string
}

// ServiceOption customises the service.
type ServiceOption func(*Service)

// WithTimezone sets the timezone used for date arithmetic when a task has none.
func WithTimezone(timezone string) ServiceOption {
	return func(s *Service) {
		if timezone != "" {
			s.timezone = timezone
		}
	}
}

// NewService builds the recurrence service.
func NewService(taskService *tasks.Service, clock tasks.Clock, opts ...ServiceOption) *Service {
	if clock == nil {
		clock = tasks.SystemClock()
	}
	service := &Service{tasks: taskService, clock: clock, timezone: "Europe/Moscow"}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Next creates the following occurrence of a completed recurring task.
//
// It is safe to call for any task: tasks without a repeat rule are ignored. When
// the rule has an end date and the series is over, nothing is created.
func (s *Service) Next(ctx context.Context, completed tasks.TaskWithVersion) error {
	if completed.Task == nil || completed.Task.Repeat == nil {
		return nil
	}
	repeat := *completed.Task.Repeat
	if err := repeat.Validate(); err != nil {
		return err
	}

	anchor, err := s.anchorDate(completed.Task)
	if err != nil {
		return err
	}
	next, err := repeat.Next(anchor)
	if err != nil {
		return err
	}
	if next.IsZero() {
		// The rule has an end date and the series is finished.
		return nil
	}

	loc, err := completed.Task.Location()
	if err != nil {
		loc = time.UTC
	}
	shift := int(next.Time(loc).Sub(anchor.Time(loc)).Hours() / 24)

	input := tasks.CreateTaskInput{
		Title:       completed.Task.Title,
		Priority:    tasks.Some(completed.Task.Priority),
		Timezone:    tasks.Some(completed.Task.Timezone),
		Tags:        append([]string(nil), completed.Task.Tags...),
		Repeat:      tasks.Some(repeat),
		PlannedDate: tasks.Some(next),
	}
	if completed.Task.Note != nil {
		input.Note = tasks.Some(*completed.Task.Note)
	}
	if completed.Task.ExpectedResult != nil {
		input.ExpectedResult = tasks.Some(*completed.Task.ExpectedResult)
	}
	if completed.Task.EstimateMinutes != nil {
		input.EstimateMinutes = tasks.Some(*completed.Task.EstimateMinutes)
	}
	if completed.Task.DueDate != nil && !completed.Task.DueDate.IsZero() {
		input.DueDate = tasks.Some(shiftDate(*completed.Task.DueDate, shift))
	}
	if completed.Task.DueAt != nil {
		if shifted := completed.Task.DueAt.AddDate(0, 0, shift); shifted.After(s.clock.Now()) {
			input.DueAt = tasks.Some(shifted)
		}
	}
	parent := completed.Task.ID
	input.RepeatParentID = tasks.Some(parent)

	_, err = s.tasks.CreateTask(ctx, input, task.ActorSystem)
	if err != nil {
		return fmt.Errorf("create next occurrence: %w", err)
	}
	return nil
}

// anchorDate is the occurrence date the next one is computed from.
func (s *Service) anchorDate(value *task.Task) (task.CalendarDate, error) {
	if value.PlannedDate != nil && !value.PlannedDate.IsZero() {
		return *value.PlannedDate, nil
	}
	if value.DueDate != nil && !value.DueDate.IsZero() {
		return *value.DueDate, nil
	}
	loc, err := value.Location()
	if err != nil {
		loc = time.UTC
	}
	moment := value.UpdatedAt
	if value.CompletedAt != nil {
		moment = *value.CompletedAt
	}
	local := moment.In(loc)
	return task.NewCalendarDate(local.Year(), local.Month(), local.Day())
}

// shiftDate moves a calendar date by whole days.
func shiftDate(date task.CalendarDate, days int) task.CalendarDate {
	shifted := date.Time(time.UTC).AddDate(0, 0, days)
	built, err := task.NewCalendarDate(shifted.Year(), shifted.Month(), shifted.Day())
	if err != nil {
		return date
	}
	return built
}

// Recommendation is advice about a habit that is not working as planned.
type Recommendation struct {
	TaskID       int64  `json:"task_id"`
	Title        string `json:"title"`
	Repeat       string `json:"repeat"`
	Samples      int    `json:"samples"`
	AverageDelay int    `json:"average_delay_days"`
	Advice       string `json:"advice,omitempty"`
}

// Review checks whether a recurring task keeps slipping and proposes a change.
//
// A habit that is consistently completed later than planned is not a failure: the
// interval is simply wrong for this person. The advice is a proposal — the rule
// changes only when the user agrees.
func (s *Service) Review(ctx context.Context, taskID int64) (Recommendation, error) {
	found, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return Recommendation{}, err
	}
	if found.Task.Repeat == nil {
		return Recommendation{TaskID: taskID, Title: found.Task.Title}, nil
	}
	result := Recommendation{
		TaskID: taskID,
		Title:  found.Task.Title,
		Repeat: found.Task.Repeat.Describe(),
	}

	history, err := s.tasks.ListTasks(ctx, tasks.TaskFilter{
		Statuses: []task.Status{task.StatusCompleted},
		Search:   tasks.Some(found.Task.Title),
		Limit:    tasks.Some(20),
	})
	if err != nil {
		return Recommendation{}, err
	}

	delays := make([]int, 0, len(history.Tasks))
	for _, item := range history.Tasks {
		if item.Task.Repeat == nil || item.Task.PlannedDate == nil || item.Task.CompletedAt == nil {
			continue
		}
		delta := int(item.Task.CompletedAt.Sub(item.Task.PlannedDate.Time(time.UTC)).Hours() / 24)
		if delta < 0 {
			delta = 0
		}
		delays = append(delays, delta)
	}
	if len(delays) < 3 {
		result.Advice = "Пока слишком мало повторений, чтобы судить."
		return result, nil
	}
	sum := 0
	for _, delay := range delays {
		sum += delay
	}
	average := sum / len(delays)
	result.Samples = len(delays)
	result.AverageDelay = average
	switch {
	case average > found.Task.Repeat.Window()+1:
		result.Advice = "Дело регулярно сдвигается: увеличьте интервал или разрешите перенос на несколько дней."
	case average > 1:
		result.Advice = "Дело часто выполняется позже плана: добавьте окно переноса в 1-2 дня."
	default:
		result.Advice = "Ритм соблюдается, менять ничего не нужно."
	}
	return result, nil
}
