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

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
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
	// Proposal is the concrete next action the user can accept, change or
	// reject. Roadmap stage 5: an observation without a proposed action is
	// only half of an initiative.
	Proposal string `json:"proposal,omitempty"`
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
	Outreach() OutreachRepository
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

// GoalsWatcher is the slice of the goal service the stalled-goal detector
// needs. Roadmap stage 4: a goal that stopped moving is a real deviation, so
// it surfaces in the same insight stream as overdue work.
type GoalsWatcher interface {
	Stalled(ctx context.Context) ([]appgoals.StalledGoal, error)
}

// DeadlineRiskWatcher is the slice of the goal service that reports goals
// whose target date is approaching faster than the work is moving.
type DeadlineRiskWatcher interface {
	DeadlineRisks(ctx context.Context) ([]appgoals.DeadlineRisk, error)
}

// CalendarWatcher is the slice of the calendar read model the day-shape
// detectors need. Roadmap stage 5: a plan is only honest when it knows where
// the meetings are, so the calendar joins the same cross-reference as tasks
// and goals.
type CalendarWatcher interface {
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]appevents.EventWithVersion, error)
}

// Agreement is one durable agreement stored in memory.
type Agreement struct {
	ID        int64
	Content   string
	UpdatedAt time.Time
}

// MemoryWatcher is the slice of the memory service the forgotten-commitment
// detector needs. An agreement the user has not touched in weeks may still be
// valid — or a forgotten obligation nobody is tracking.
type MemoryWatcher interface {
	Agreements(ctx context.Context) ([]Agreement, error)
}

// Option customises the proactive service.
type Option func(*Service)

// WithGoals attaches the goal service used by the stalled-goal detector.
// Without it the detector is skipped and nothing else changes.
func WithGoals(watcher GoalsWatcher) Option {
	return func(s *Service) { s.goals = watcher }
}

// WithDeadlineRisks attaches the goal deadline-risk watcher.
func WithDeadlineRisks(watcher DeadlineRiskWatcher) Option {
	return func(s *Service) { s.deadlineRisks = watcher }
}

// WithCalendar attaches the calendar read model used by the day-shape detectors.
func WithCalendar(watcher CalendarWatcher) Option {
	return func(s *Service) { s.calendar = watcher }
}

// WithMemory attaches the memory slice used by the forgotten-commitment detector.
func WithMemory(watcher MemoryWatcher) Option {
	return func(s *Service) { s.memory = watcher }
}

// Service computes and manages insights.
type Service struct {
	uow           UnitOfWork
	tasks         TaskLister
	inbox         InboxCounter
	goals         GoalsWatcher
	deadlineRisks DeadlineRiskWatcher
	calendar      CalendarWatcher
	memory        MemoryWatcher
	clock         Clock
	timezone      string
}

// NewService builds the proactive service.
func NewService(uow UnitOfWork, taskLister TaskLister, inboxCounter InboxCounter, clock Clock, timezone string, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	if timezone == "" {
		timezone = tasks.DefaultTimezone
	}
	s := &Service{uow: uow, tasks: taskLister, inbox: inboxCounter, clock: clock, timezone: timezone}
	for _, opt := range opts {
		opt(s)
	}
	return s
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

	// Cross-referencing is deliberately failure-tolerant: a calendar or memory
	// hiccup degrades the day's picture, it must not erase the task insights.
	var events []appevents.EventWithVersion
	if s.calendar != nil {
		if found, err := s.calendar.ListRange(ctx, dayStart, dayEnd, nil); err == nil {
			events = found
		}
	}
	var agreements []Agreement
	if s.memory != nil {
		if found, err := s.memory.Agreements(ctx); err == nil {
			agreements = found
		}
	}

	insights := s.detect(open, inboxCount, now, dayStart, dayEnd, events, agreements)
	// A stopped goal is discovered from its own journal, not from the task
	// list, so the detector runs separately and never fails the day's insights.
	if s.goals != nil {
		insights = append(insights, s.stalledGoalInsights(ctx)...)
	}
	if s.deadlineRisks != nil {
		insights = append(insights, s.deadlineRiskInsights(ctx)...)
	}
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
func (s *Service) detect(open []tasks.TaskWithVersion, inboxCount int, now, dayStart, dayEnd time.Time, events []appevents.EventWithVersion, agreements []Agreement) []Insight {
	insights := make([]Insight, 0, 8)

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
			Detail:   fmt.Sprintf("%d задач(и) просрочены", len(overdue)),
			Count:    len(overdue),
			TaskIDs:  limitIDs(overdue, 10),
			Proposal: "Перенести просроченное на реальные даты или закрыть",
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
			Proposal: "Пройтись по списку и зафиксировать порядок",
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
			Proposal: "Перенести часть на другие дни заранее",
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
			Proposal: "Назначить срок каждой или убрать лишнее",
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
			Proposal: "Разнести дедлайны по времени или объединить в одну задачу",
		})
	}

	// Roadmap stage 5: the calendar joins the cross-reference. A task due in
	// the middle of a meeting cannot be done on time — the clash is real even
	// though both items alone look perfectly fine.
	if clash := taskEventClashInsight(open, events, dayStart, dayEnd); clash != nil {
		insights = append(insights, *clash)
	}
	if busy := busyDayInsight(events, len(today), dayStart); busy != nil {
		insights = append(insights, *busy)
	}
	if free := freeCapacityInsight(open, events, now, dayStart, dayEnd); free != nil {
		insights = append(insights, *free)
	}
	if prepare := dueTomorrowInsight(open, dayEnd); prepare != nil {
		insights = append(insights, *prepare)
	}
	if forgotten := forgottenAgreementsInsight(agreements, now); forgotten != nil {
		insights = append(insights, *forgotten)
	}

	return insights
}

// meetingOverlapTolerance absorbs boundaries: a task due exactly at a
// meeting's end is not a clash, a task due mid-meeting is.
const meetingOverlapTolerance = 5 * time.Minute

// busyDayMeetingHours is the meeting load after which a day stops being a
// workday with meetings and becomes a meeting day with fragments of work.
const busyDayMeetingHours = 5 * time.Hour

// freeCapacityWindow is the minimum continuous free time that makes an
// "use it" suggestion honest: shorter gaps are fragments, not capacity.
const freeCapacityWindow = 90 * time.Minute

// taskEventClashInsight finds open tasks whose exact deadline falls inside a
// meeting happening today.
func taskEventClashInsight(open []tasks.TaskWithVersion, events []appevents.EventWithVersion, dayStart, dayEnd time.Time) *Insight {
	clashed := make([]int64, 0)
	for _, item := range open {
		if item.Task.DueAt == nil {
			continue
		}
		due := item.Task.DueAt.In(dayStart.Location())
		if due.Before(dayStart) || !due.Before(dayEnd) {
			continue
		}
		for _, ev := range events {
			starts := ev.Event.StartsAt.In(dayStart.Location())
			ends := ev.Event.EndsAt.In(dayStart.Location())
			if due.After(starts.Add(-meetingOverlapTolerance)) && due.Before(ends.Add(meetingOverlapTolerance)) {
				clashed = append(clashed, item.Task.ID)
				break
			}
		}
	}
	if len(clashed) == 0 {
		return nil
	}
	return &Insight{
		Key:      "task-event-clash",
		Kind:     "task-event-clash",
		Severity: SeverityWarning,
		Title:    "Дедлайн попадает на встречу",
		Detail:   fmt.Sprintf("%d задач(и) должны быть готовы прямо во время встречи", len(clashed)),
		Count:    len(clashed),
		TaskIDs:  limitIDs(clashed, 10),
		Proposal: "Перенести дедлайн или встречу заранее",
	}
}

// busyDayInsight warns when meetings consume most of the working day while
// tasks still expect it.
func busyDayInsight(events []appevents.EventWithVersion, dueToday int, dayStart time.Time) *Insight {
	if len(events) == 0 || dueToday == 0 {
		return nil
	}
	meetingMinutes := 0
	for _, ev := range events {
		minutes := int(ev.Event.EndsAt.Sub(ev.Event.StartsAt).Minutes())
		if minutes > 0 {
			meetingMinutes += minutes
		}
	}
	if meetingMinutes < int(busyDayMeetingHours.Minutes()) {
		return nil
	}
	return &Insight{
		Key:      "busy-day",
		Kind:     "busy-day",
		Severity: SeverityWarning,
		Title:    "День занят встречами",
		Detail:   fmt.Sprintf("Встречи занимают около %s, а на сегодня ещё есть задачи", humanHours(meetingMinutes)),
		Count:    len(events),
		Proposal: "Оставить на сегодня только обязательное, остальное перенести",
	}
}

// freeCapacityInsight spots an honest opportunity: a day with a real free
// window and tomorrow's work that could move into it today.
func freeCapacityInsight(open []tasks.TaskWithVersion, events []appevents.EventWithVersion, now, dayStart, dayEnd time.Time) *Insight {
	meetingMinutes := 0
	for _, ev := range events {
		if ev.Event.EndsAt.Before(now) {
			continue
		}
		minutes := int(ev.Event.EndsAt.Sub(ev.Event.StartsAt).Minutes())
		if minutes > 0 {
			meetingMinutes += minutes
		}
	}
	if meetingMinutes > int(busyDayMeetingHours.Minutes()) {
		return nil
	}
	// The opportunity is only real when there is future work that could use
	// the window: tasks due within the next two days that are not due today.
	tomorrowEnd := dayEnd.AddDate(0, 0, 1)
	ready := make([]int64, 0)
	for _, item := range open {
		if item.Task.DueAt == nil {
			continue
		}
		due := item.Task.DueAt.In(dayStart.Location())
		if !due.Before(dayEnd) && due.Before(tomorrowEnd) {
			ready = append(ready, item.Task.ID)
		}
	}
	if len(ready) == 0 {
		return nil
	}
	return &Insight{
		Key:      "free-capacity",
		Kind:     "free-capacity",
		Severity: SeverityInfo,
		Title:    "Есть свободное время сегодня",
		Detail:   fmt.Sprintf("Завтра дедлайн у %d задач(и), а сегодня для них есть свободное окно", len(ready)),
		Count:    len(ready),
		TaskIDs:  limitIDs(ready, 10),
		Proposal: "Заранее разобрать часть завтрашних задач сегодня",
	}
}

// dueTomorrowInsight nudges preparation: high-priority work becomes much
// harder to rescue the morning it is due.
func dueTomorrowInsight(open []tasks.TaskWithVersion, dayEnd time.Time) *Insight {
	ids := make([]int64, 0)
	for _, item := range open {
		if item.Task.Priority != task.PriorityHigh {
			continue
		}
		if item.Task.DueAt != nil {
			due := item.Task.DueAt.In(dayEnd.Location())
			if !due.Before(dayEnd) && due.Before(dayEnd.AddDate(0, 0, 1)) {
				ids = append(ids, item.Task.ID)
			}
			continue
		}
		if item.Task.DueDate != nil && !item.Task.DueDate.IsZero() {
			day := item.Task.DueDate.Time(dayEnd.Location()).Truncate(24 * time.Hour)
			if day.Equal(dayEnd.Truncate(24 * time.Hour)) {
				ids = append(ids, item.Task.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return &Insight{
		Key:      "due-tomorrow",
		Kind:     "due-tomorrow",
		Severity: SeverityInfo,
		Title:    "Важный дедлайн завтра",
		Detail:   fmt.Sprintf("%d важных задач(и) со сроком завтра — стоит подготовиться сегодня", len(ids)),
		Count:    len(ids),
		TaskIDs:  limitIDs(ids, 10),
		Proposal: "Запланировать время на них сегодня",
	}
}

// forgottenAgreementAfter is how long an agreement may sit untouched before
// "maybe it is still valid" turns into "probably forgotten".
const forgottenAgreementAfter = 14 * 24 * time.Hour

// forgottenAgreementsInsight surfaces durable agreements nobody has touched
// for weeks — forgotten obligations, not tracked by any task.
func forgottenAgreementsInsight(agreements []Agreement, now time.Time) *Insight {
	stale := make([]Agreement, 0)
	for _, entry := range agreements {
		if now.Sub(entry.UpdatedAt) >= forgottenAgreementAfter {
			stale = append(stale, entry)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return &Insight{
		Key:      "forgotten-agreements",
		Kind:     "forgotten-agreements",
		Severity: SeverityWarning,
		Title:    "Договорённости без движения",
		Detail:   fmt.Sprintf("%d договорённост(и) в памяти не упоминались две недели и дольше", len(stale)),
		Count:    len(stale),
		Proposal: "Проверить, актуальны ли они, и превратить живые в задачи",
	}
}

// deadlineRiskInsights turns lagging goal deadlines into one insight each;
// at most three surface so a crowded plan cannot drown the day's signals.
func (s *Service) deadlineRiskInsights(ctx context.Context) []Insight {
	risks, err := s.deadlineRisks.DeadlineRisks(ctx)
	if err != nil {
		return nil
	}
	insights := make([]Insight, 0, min(len(risks), 3))
	for _, risk := range risks {
		if len(insights) >= 3 {
			break
		}
		insights = append(insights, Insight{
			Key:      fmt.Sprintf("goal-deadline-risk-%d", risk.Goal.ID),
			Kind:     "goal-deadline-risk",
			Severity: SeverityWarning,
			Title:    fmt.Sprintf("Цель «%s»: до срока %d дн., движения нет %d дн.", risk.Goal.Title, risk.DaysLeft, risk.IdleDays),
			Detail:   "Срок приближается быстрее, чем движется работа по цели",
			Count:    1,
			Proposal: "Обсудить план цели: ускорить, переоценить срок или сократить объём",
		})
	}
	return insights
}

// stalledGoalInsights turns stopped goals into one insight each; at most three
// are surfaced so a stalled backlog cannot drown the day's signals.
func (s *Service) stalledGoalInsights(ctx context.Context) []Insight {
	stalled, err := s.goals.Stalled(ctx)
	if err != nil {
		return nil
	}
	insights := make([]Insight, 0, min(len(stalled), 3))
	for _, item := range stalled {
		if len(insights) >= 3 {
			break
		}
		reason := item.Review.Signals.StallReason
		insights = append(insights, Insight{
			Key:      fmt.Sprintf("goal-stalled-%d", item.Goal.ID),
			Kind:     "goal-stalled",
			Severity: SeverityWarning,
			Title:    fmt.Sprintf("Цель «%s» остановилась", item.Goal.Title),
			Detail:   fmt.Sprintf("%s — стоит обсудить, продолжать ли работу, и при необходимости скорректировать план", reason),
			Count:    1,
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

// humanHours renders a minute count in Russian-friendly short form.
func humanHours(minutes int) string {
	hours := minutes / 60
	remainder := minutes % 60
	if remainder == 0 {
		return fmt.Sprintf("%d ч", hours)
	}
	if hours == 0 {
		return fmt.Sprintf("%d мин", remainder)
	}
	return fmt.Sprintf("%d ч %d мин", hours, remainder)
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
