package planning

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/planning"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Errors returned by the planner.
var (
	// ErrNoTask is returned when a placement references a task that is missing.
	ErrNoTask = errors.New("task not found")
	// ErrTaskNotOpen is returned when a placement references a finished task.
	ErrTaskNotOpen = errors.New("only open tasks can be planned")
	// ErrPlacementInvalid is returned when a placement is malformed.
	ErrPlacementInvalid = errors.New("placement must have a task, a start and an end before it")
	// ErrPlacementOutsideWindow is returned when a placement is outside working hours.
	ErrPlacementOutsideWindow = errors.New("placement must be inside the working day")
	// ErrPlacementConflict is returned when a placement overlaps an appointment or the user's own block.
	ErrPlacementConflict = errors.New("placement overlaps an existing commitment")
	// ErrPlacementInPast is returned when a placement would reserve time that has already passed.
	ErrPlacementInPast = errors.New("placement must not start in the past")
	// ErrPlacementAfterDue is returned when a placement ends after an exact deadline.
	ErrPlacementAfterDue = errors.New("placement must end before the task deadline")
)

const (
	// maxPlanCandidates bounds how many open tasks are considered for one day.
	maxPlanCandidates = 300
	// maxHorizonDays bounds the replanning horizon.
	maxHorizonDays = 30
	// defaultHorizonDays is the replanning horizon used when the caller is silent.
	defaultHorizonDays = 7
)

// Service implements the time-management use cases.
type Service struct {
	uow      UnitOfWork
	clock    Clock
	timezone string
	pub      Publisher
}

// ServiceOption customises a Service.
type ServiceOption func(*Service)

// WithPublisher attaches an event publisher. Changes publish after the
// transaction commits, so subscribers never observe rolled-back work.
func WithPublisher(publisher Publisher) ServiceOption {
	return func(s *Service) { s.pub = publisher }
}

// WithTimezone sets the IANA timezone used to interpret working hours.
func WithTimezone(timezone string) ServiceOption {
	return func(s *Service) {
		if timezone != "" {
			s.timezone = timezone
		}
	}
}

// NewService builds the planner.
func NewService(uow UnitOfWork, clock Clock, opts ...ServiceOption) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	service := &Service{uow: uow, clock: clock, timezone: "Europe/Moscow"}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Location returns the timezone the planner works in.
func (s *Service) Location() *time.Location {
	loc, err := time.LoadLocation(s.timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// PlacementInput is one requested work block.
type PlacementInput struct {
	TaskID int64     `json:"task_id"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

// PlanItem is a block shown in the interface.
type PlanItem struct {
	BlockID        int64                `json:"block_id,omitempty"`
	TaskID         int64                `json:"task_id"`
	Title          string               `json:"title"`
	Priority       task.Priority        `json:"priority"`
	Start          time.Time            `json:"start"`
	End            time.Time            `json:"end"`
	Source         planning.BlockSource `json:"source,omitempty"`
	Status         planning.BlockStatus `json:"status,omitempty"`
	Estimate       int                  `json:"estimate_minutes,omitempty"`
	EstimateSource EstimateSource       `json:"estimate_source,omitempty"`
	Recurring      bool                 `json:"recurring,omitempty"`
	DueAt          *time.Time           `json:"due_at,omitempty"`
	DueDate        string               `json:"due_date,omitempty"`
}

// BusyView is an occupied range shown next to the plan.
type BusyView struct {
	Label  string    `json:"label"`
	Kind   string    `json:"kind"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	TaskID int64     `json:"task_id,omitempty"`
}

// UnplacedView explains why a task was not scheduled.
type UnplacedView struct {
	TaskID      int64          `json:"task_id"`
	Title       string         `json:"title"`
	EstimateMin int            `json:"estimate_minutes"`
	Source      EstimateSource `json:"estimate_source,omitempty"`
	Reason      string         `json:"reason"`
}

// DayPlan is the read model of one day.
type DayPlan struct {
	Date           string         `json:"date"`
	Working        bool           `json:"working"`
	Past           bool           `json:"past"`
	WorkdayStart   string         `json:"workday_start"`
	WorkdayEnd     string         `json:"workday_end"`
	FreeMinutes    int            `json:"free_minutes"`
	PlannedMinutes int            `json:"planned_minutes"`
	Busy           []BusyView     `json:"busy"`
	Blocks         []PlanItem     `json:"blocks"`
	Proposed       []PlanItem     `json:"proposed"`
	Unplaced       []UnplacedView `json:"unplaced"`
}

// ApplyInput applies a set of placements.
//
// ReplaceDates lists the days the request is allowed to change. It never wipes a
// day: only blocks of the same task on that day, and automatic blocks that collide
// with a new placement, are replaced. Everything else stays, so accepting a
// proposal for one more task cannot destroy an already accepted plan.
type ApplyInput struct {
	ReplaceDates []string         `json:"replace_dates"`
	Items        []PlacementInput `json:"items"`
}

// ApplyResult reports what was written.
type ApplyResult struct {
	Applied []PlanItem `json:"applied"`
	Skipped []string   `json:"skipped"`
}

// Move is one proposed change in a replanning pass.
type Move struct {
	TaskID   int64  `json:"task_id"`
	Title    string `json:"title"`
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
	Reason   string `json:"reason"`
	Estimate int    `json:"estimate_minutes"`
}

// ProposedPlacement is a placement the user may accept as-is.
type ProposedPlacement struct {
	TaskID int64     `json:"task_id"`
	Date   string    `json:"date"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

// DayLoad summarises one day of the replanning horizon.
type DayLoad struct {
	Date        string `json:"date"`
	Working     bool   `json:"working"`
	FreeMinutes int    `json:"free_minutes"`
	PlannedMin  int    `json:"planned_minutes"`
}

// Proposal is the result of replanning.
type Proposal struct {
	GeneratedAt time.Time           `json:"generated_at"`
	HorizonDays int                 `json:"horizon_days"`
	Moves       []Move              `json:"moves"`
	Placements  []ProposedPlacement `json:"placements"`
	Unplaced    []UnplacedView      `json:"unplaced"`
	Kept        []KeptTaskView      `json:"already_placed"`
	Days        []DayLoad           `json:"days"`
	Summary     string              `json:"summary"`
}

// KeptTaskView reports work that already has its time and is therefore not
// proposed again. It is shown so the interface can explain the omission instead of
// silently leaving a task out of the plan.
type KeptTaskView struct {
	TaskID int64     `json:"task_id"`
	Title  string    `json:"title"`
	Reason string    `json:"reason"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Source string    `json:"source"`
}

// PlanDay returns the day's commitments, its existing blocks and a proposal for
// the remaining open tasks. Nothing is written: the user accepts a plan
// explicitly.
func (s *Service) PlanDay(ctx context.Context, date task.CalendarDate) (DayPlan, error) {
	if date.IsZero() || !date.IsValid() {
		return DayPlan{}, fmt.Errorf("invalid date")
	}
	now := s.clock.Now()
	loc := s.Location()
	prefs, err := s.LoadPreferences(ctx)
	if err != nil {
		return DayPlan{}, err
	}

	var plan DayPlan
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		computed, err := s.planDayTx(ctx, tx, date, prefs, loc, now)
		plan = computed
		return err
	})
	return plan, err
}

func (s *Service) planDayTx(
	ctx context.Context,
	tx Transaction,
	date task.CalendarDate,
	prefs Preferences,
	loc *time.Location,
	now time.Time,
) (DayPlan, error) {
	window, working := prefs.WindowOn(date, loc)
	plan := DayPlan{
		Date:         date.String(),
		Working:      working,
		WorkdayStart: formatClock(prefs.Start),
		WorkdayEnd:   formatClock(prefs.End),
		Busy:         []BusyView{},
		Blocks:       []PlanItem{},
		Proposed:     []PlanItem{},
		Unplaced:     []UnplacedView{},
	}

	dayStart := date.Time(loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	events, err := tx.Events().ListRange(ctx, dayStart.UTC(), dayEnd.UTC(), nil)
	if err != nil {
		return DayPlan{}, err
	}
	blocks, err := tx.Blocks().ListRange(ctx, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		return DayPlan{}, err
	}
	tasksByID, err := s.openTasks(ctx, tx)
	if err != nil {
		return DayPlan{}, err
	}

	fixed := make([]Busy, 0, len(events)+len(blocks))
	for _, item := range events {
		label := item.Event.Title
		busy := Busy{Interval: Interval{Start: item.Event.StartsAt, End: item.Event.EndsAt}, Label: label, Kind: "event"}
		if item.Event.AllDay {
			busy.Interval = Interval{Start: dayStart, End: dayEnd}
		}
		fixed = append(fixed, busy)
		plan.Busy = append(plan.Busy, BusyView{Label: label, Kind: "event", Start: busy.Start, End: busy.End})
	}

	for _, block := range blocks {
		value := tasksByID[block.TaskID]
		title := ""
		priority := task.PriorityMedium
		estimate := 0
		recurring := false
		var dueAt *time.Time
		dueDate := ""
		if value != nil {
			title = value.Task.Title
			priority = value.Task.Priority
			recurring = value.Task.Repeat != nil
			dueAt = value.Task.DueAt
			if value.Task.DueDate != nil && !value.Task.DueDate.IsZero() {
				dueDate = value.Task.DueDate.String()
			}
			if value.Task.EstimateMinutes != nil {
				estimate = *value.Task.EstimateMinutes
			}
		}
		plan.Blocks = append(plan.Blocks, PlanItem{
			BlockID:   block.ID,
			TaskID:    block.TaskID,
			Title:     title,
			Priority:  priority,
			Start:     block.StartsAt,
			End:       block.EndsAt,
			Source:    block.Source,
			Status:    block.Status,
			Estimate:  estimate,
			Recurring: recurring,
			DueAt:     dueAt,
			DueDate:   dueDate,
		})
		plan.PlannedMinutes += int(block.Duration().Minutes())
		// A block is time the user already spends on the task, whoever placed it:
		// the plan works around it and the task is not proposed twice.
		fixed = append(fixed, Busy{
			Interval: Interval{Start: block.StartsAt, End: block.EndsAt},
			Label:    title,
			Kind:     "block",
			BlockID:  block.ID,
			TaskID:   block.TaskID,
		})
		plan.Busy = append(plan.Busy, BusyView{Label: title, Kind: "block", Start: block.StartsAt, End: block.EndsAt, TaskID: block.TaskID})
	}

	free := FreeSlots(window, fixed, prefs.Buffer, prefs.MinSlot)
	// Time that has already passed is not free: a plan for the current day starts
	// at the current moment, never earlier.
	if sameLocalDay(now, date, loc) {
		free = ClipFrom(free, nextMinute(now))
		free = dropShortSlots(free, prefs.MinSlot)
	}
	for _, slot := range free {
		plan.FreeMinutes += int(slot.Duration().Minutes())
	}

	if !working {
		return plan, nil
	}
	if dateIsPast(date, now, loc) {
		// The day is over. Proposing anything for it would be a plan for the past,
		// and its remaining "free" time is not free any more.
		plan.Past = true
		plan.FreeMinutes = 0
		return plan, nil
	}

	alreadyPlanned := make(map[int64]bool, len(blocks))
	for _, block := range blocks {
		alreadyPlanned[block.TaskID] = true
	}

	history, err := s.loadHistory(ctx, tx)
	if err != nil {
		return DayPlan{}, err
	}
	candidates := s.candidatesFor(ctx, tx, tasksByID, date, now, alreadyPlanned, prefs, history, loc)
	placements, unplaced := Schedule(free, candidates, prefs.Buffer)
	for _, placement := range placements {
		value := tasksByID[placement.TaskID]
		item := PlanItem{
			TaskID:         placement.TaskID,
			Title:          placement.Title,
			Priority:       placement.Priority,
			Start:          placement.Start,
			End:            placement.End,
			Estimate:       int(placement.Estimate.Minutes()),
			EstimateSource: placement.EstimateSource,
		}
		if value != nil {
			item.Recurring = value.Task.Repeat != nil
			item.DueAt = value.Task.DueAt
			if value.Task.DueDate != nil && !value.Task.DueDate.IsZero() {
				item.DueDate = value.Task.DueDate.String()
			}
		}
		plan.Proposed = append(plan.Proposed, item)
	}
	for _, item := range unplaced {
		plan.Unplaced = append(plan.Unplaced, UnplacedView{
			TaskID:      item.TaskID,
			Title:       item.Title,
			EstimateMin: int(item.Estimate.Minutes()),
			Source:      item.EstimateSource,
			Reason:      item.Reason,
		})
	}
	return plan, nil
}

// candidatesFor selects the open tasks that belong to the given day.
//
// Everything open is considered, ordered by urgency: overdue work first, then
// work due today, then work planned for the day, then the rest of the backlog.
// The planner fills the free time it has, so a day with no deadlines still gets a
// sensible order instead of an empty plan.
func (s *Service) candidatesFor(
	ctx context.Context,
	tx Transaction,
	tasksByID map[int64]*tasks.TaskWithVersion,
	date task.CalendarDate,
	now time.Time,
	alreadyPlanned map[int64]bool,
	prefs Preferences,
	history *historyIndex,
	loc *time.Location,
) []Candidate {
	today, err := dateOf(now, loc)
	if err != nil {
		today = date
	}

	candidates := make([]Candidate, 0, len(tasksByID))
	for id, value := range tasksByID {
		if alreadyPlanned[id] {
			continue
		}
		item := value.Task
		resolved := s.resolveEstimate(item, prefs, history)

		candidate := Candidate{
			TaskID:         item.ID,
			Title:          item.Title,
			Priority:       item.Priority,
			Estimate:       resolved.Value,
			EstimateSource: resolved.Source,
			DueAt:          item.DueAt,
			CreatedAt:      item.CreatedAt,
			Recurring:      item.Repeat != nil,
			Urgency:        4,
		}
		// An exact deadline limits the block, and a deadline that already passed
		// is no longer a constraint: the work is simply overdue.
		if item.DueAt != nil && item.DueAt.After(now) {
			deadline := item.DueAt.UTC()
			candidate.HardDeadline = &deadline
			candidate.DeadlineLabel = "дедлайна " + deadline.In(loc).Format("15:04")
		}
		if item.DueDate != nil && !item.DueDate.IsZero() {
			due := *item.DueDate
			candidate.DueDate = &due
		}
		if item.PlannedDate != nil && !item.PlannedDate.IsZero() {
			planned := *item.PlannedDate
			candidate.PlannedDate = &planned
		}

		switch {
		case item.IsOverdue(now):
			candidate.Urgency = 0
		case candidate.DueDate != nil && candidate.DueDate.String() <= date.String():
			candidate.Urgency = 1
		case candidate.DueAt != nil && sameLocalDay(*candidate.DueAt, date, loc):
			candidate.Urgency = 1
		case sameCalendarDate(candidate.PlannedDate, date):
			candidate.Urgency = 3
		}

		// A recurring task is proposed on the days its flexible window covers:
		// this is what makes an interval flexible instead of rigid.
		if item.Repeat != nil && !sameCalendarDate(candidate.PlannedDate, date) {
			if !repeatCovers(*item.Repeat, candidate.PlannedDate, date, today) {
				continue
			}
			candidate.Projected = true
			if candidate.Urgency > 3 {
				candidate.Urgency = 3
			}
		}

		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].CreatedAt.Before(candidates[b].CreatedAt) })
	return candidates
}

// Apply writes the accepted placements.
//
// The whole set is validated before anything is written: a plan that overlaps an
// appointment or the user's own block is rejected instead of being silently
// squeezed in. Existing automatically planned blocks of the affected days are
// replaced, manual ones are never touched.
func (s *Service) Apply(ctx context.Context, in ApplyInput) (ApplyResult, error) {
	if len(in.Items) == 0 && len(in.ReplaceDates) == 0 {
		return ApplyResult{}, ErrPlacementInvalid
	}
	now := s.clock.Now()
	loc := s.Location()
	prefs, err := s.LoadPreferences(ctx)
	if err != nil {
		return ApplyResult{}, err
	}

	var result ApplyResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		applied, skipped, err := s.applyTx(ctx, tx, in, prefs, loc, now)
		if err != nil {
			return err
		}
		result = ApplyResult{Applied: applied, Skipped: skipped}
		return nil
	})
	if err != nil {
		return ApplyResult{}, err
	}
	if s.pub != nil && len(result.Applied) > 0 {
		s.pub.Publish("plan.updated", map[string]any{"applied": len(result.Applied)})
	}
	return result, nil
}

// setPlannedDate keeps the task's planned date consistent with its block.
func (s *Service) setPlannedDate(ctx context.Context, tx Transaction, found tasks.TaskWithVersion, date task.CalendarDate, now time.Time) error {
	if found.Task.PlannedDate != nil && !found.Task.PlannedDate.IsZero() && found.Task.PlannedDate.String() == date.String() {
		return nil
	}
	updated := cloneTask(found.Task)
	if err := updated.Update(now, task.WithPlannedDate(date)); err != nil {
		return err
	}
	if _, err := tx.Tasks().Update(ctx, updated.ID, found.Version, updated); err != nil {
		return err
	}
	return nil
}

// Replan proposes what to do with overdue and deadline-bound work.
//
// The planner never moves existing commitments: the proposal only concerns open
// tasks and the user's own work blocks on free time. Nothing is written until the
// user accepts.
func (s *Service) Replan(ctx context.Context, horizonDays int) (Proposal, error) {
	if horizonDays <= 0 {
		horizonDays = defaultHorizonDays
	}
	if horizonDays > maxHorizonDays {
		horizonDays = maxHorizonDays
	}
	now := s.clock.Now()
	loc := s.Location()
	prefs, err := s.LoadPreferences(ctx)
	if err != nil {
		return Proposal{}, err
	}

	var proposal Proposal
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		computed, err := s.replanTx(ctx, tx, prefs, loc, now, horizonDays)
		proposal = computed
		return err
	})
	return proposal, err
}

// openTasks loads open tasks once per transaction.
func (s *Service) openTasks(ctx context.Context, tx Transaction) (map[int64]*tasks.TaskWithVersion, error) {
	filter := tasks.TaskFilter{
		Statuses: []task.Status{task.StatusOpen},
		Limit:    tasks.Some(maxPlanCandidates),
	}
	result, err := tx.Tasks().List(ctx, filter)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*tasks.TaskWithVersion, len(result.Tasks))
	for i := range result.Tasks {
		item := result.Tasks[i]
		byID[item.Task.ID] = &item
	}
	return byID, nil
}

// RemoveBlock deletes a planned block. The task becomes unplanned; nothing else
// changes, so the user can drop reserved time without touching the work itself.
func (s *Service) RemoveBlock(ctx context.Context, blockID int64) error {
	if blockID <= 0 {
		return ErrPlacementInvalid
	}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Blocks().Delete(ctx, blockID)
	})
	if err != nil {
		return err
	}
	if s.pub != nil {
		s.pub.Publish("plan.updated", map[string]any{"removed_block": blockID})
	}
	return nil
}

// SyncTaskReminders re-derives the reminders of one task.
//
// It is called by the task engine after every mutation, so reminder moments can
// never drift away from the deadline, priority or estimate they were computed
// from.
func (s *Service) SyncTaskReminders(ctx context.Context, value task.Task) error {
	now := s.clock.Now()
	planned := planning.PlanReminders(&value, now)
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		if value.Status != task.StatusOpen {
			return tx.Reminders().DismissForTask(ctx, value.ID)
		}
		return tx.Reminders().ReplaceForTask(ctx, value.ID, planned)
	})
}

// DueReminders returns reminders that should be published now: due, not yet
// published, and belonging to work that is still open.
func (s *Service) DueReminders(ctx context.Context, until time.Time) ([]ReminderView, error) {
	return s.dueReminders(ctx, until, false)
}

// ActionableReminders returns everything the client still has to show.
//
// Unlike DueReminders it includes reminders whose live event was already
// published, so a client that was closed, offline or reconnecting still receives
// them. Delivery is recorded only when the client acknowledges the reminder.
func (s *Service) ActionableReminders(ctx context.Context, until time.Time) ([]ReminderView, error) {
	return s.dueReminders(ctx, until, true)
}

func (s *Service) dueReminders(ctx context.Context, until time.Time, actionable bool) ([]ReminderView, error) {
	var out []ReminderView
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var (
			pending []planning.Reminder
			err     error
		)
		if actionable {
			pending, err = tx.Reminders().ListActionable(ctx, until.UTC())
		} else {
			pending, err = tx.Reminders().ListPending(ctx, until.UTC())
		}
		if err != nil {
			return err
		}
		for _, reminder := range pending {
			found, err := tx.Tasks().FindByID(ctx, reminder.TaskID)
			if err != nil {
				continue
			}
			if found.Task.Status != task.StatusOpen {
				// A finished task must not be announced, and its pending
				// reminders would otherwise stay forever.
				if err := tx.Reminders().DismissForTask(ctx, reminder.TaskID); err != nil {
					return err
				}
				continue
			}
			out = append(out, ReminderView{Reminder: reminder, Task: found.Task})
		}
		return nil
	})
	return out, err
}

// MarkReminderNotified records that the live event was published.
func (s *Service) MarkReminderNotified(ctx context.Context, id int64, at time.Time) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Reminders().MarkNotified(ctx, id, at.UTC())
	})
}

// MarkReminderDelivered records that the user was shown the reminder.
func (s *Service) MarkReminderDelivered(ctx context.Context, id int64, at time.Time) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Reminders().MarkDelivered(ctx, id, at.UTC())
	})
}

// ReconcileReminders re-derives the reminders of open tasks that have a deadline
// but no pending reminder at all.
//
// Reminder regeneration runs after every task change and is not allowed to fail
// that change, so a failure there could otherwise leave a task silently without
// notifications. This pass makes that state self-healing instead of permanent.
func (s *Service) ReconcileReminders(ctx context.Context) (int, error) {
	now := s.clock.Now()
	repaired := 0
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		result, err := tx.Tasks().List(ctx, tasks.TaskFilter{
			Statuses: []task.Status{task.StatusOpen},
			Limit:    tasks.Some(maxPlanCandidates),
		})
		if err != nil {
			return err
		}
		for _, item := range result.Tasks {
			value := item.Task
			if value.DueAt == nil && (value.DueDate == nil || value.DueDate.IsZero()) {
				continue
			}
			planned := planning.PlanReminders(value, now)
			if len(planned) == 0 {
				continue
			}
			existing, err := tx.Reminders().ListForTask(ctx, value.ID)
			if err != nil {
				return err
			}
			pending := 0
			for _, reminder := range existing {
				if reminder.Status == planning.ReminderPending {
					pending++
				}
			}
			if pending > 0 {
				continue
			}
			if err := tx.Reminders().ReplaceForTask(ctx, value.ID, planned); err != nil {
				return err
			}
			repaired++
		}
		return nil
	})
	return repaired, err
}

// RemindersOf returns the planned reminders of one task.
func (s *Service) RemindersOf(ctx context.Context, taskID int64) ([]planning.Reminder, error) {
	var out []planning.Reminder
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Reminders().ListForTask(ctx, taskID)
		out = found
		return err
	})
	return out, err
}

// ReminderView pairs a reminder with the task it refers to.
type ReminderView struct {
	Reminder planning.Reminder
	Task     *task.Task
}

// EstimateSuggestion is a duration suggestion based on past work.
type EstimateSuggestion struct {
	Minutes  int      `json:"minutes"`
	Source   string   `json:"source"` // history, default, unknown
	Samples  int      `json:"samples"`
	BasedOn  []string `json:"based_on,omitempty"`
	Question string   `json:"question,omitempty"`
}

// SuggestEstimate estimates how long similar work took before.
//
// It follows the same rule as the planner: an estimate the user stated wins, then
// the median of similar completed work, then the configured default. When only the
// default is left the answer says so and carries the question to ask, so an
// invented duration never looks like a known one.
func (s *Service) SuggestEstimate(ctx context.Context, title string, tags []string) (EstimateSuggestion, error) {
	prefs, err := s.LoadPreferences(ctx)
	if err != nil {
		return EstimateSuggestion{}, err
	}

	var history *historyIndex
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		loaded, err := s.loadHistory(ctx, tx)
		history = loaded
		return err
	})
	if err != nil {
		return EstimateSuggestion{}, err
	}

	samples := history.samples(title, tags)
	if len(samples) >= minHistorySamples {
		basedOn := make([]string, 0, 3)
		for _, sample := range samples {
			if len(basedOn) >= 3 {
				break
			}
			basedOn = append(basedOn, sample.Title)
		}
		return EstimateSuggestion{
			Minutes: medianMinutes(samples),
			Source:  string(EstimateHistory),
			Samples: len(samples),
			BasedOn: basedOn,
		}, nil
	}

	minutes := int(prefs.DefaultEstimate.Minutes())
	return EstimateSuggestion{
		Minutes: minutes,
		Source:  string(EstimateDefault),
		Samples: len(samples),
		Question: "Похожих задач в истории нет. Планирую " + formatDuration(prefs.DefaultEstimate) +
			" по умолчанию — уточните длительность, если она другая.",
	}, nil
}

// significantWords keeps the words that carry meaning, so "Позвонить в банк" and
// "Звонок банку" are recognised as similar.
func significantWords(title string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !isLetterOrDigit(r)
	}) {
		if len([]rune(word)) < 4 {
			continue
		}
		out[strings.TrimSuffix(strings.TrimSuffix(word, "и"), "а")] = true
	}
	return out
}

func sharesWords(words map[string]bool, title string) bool {
	for word := range significantWords(title) {
		if words[word] {
			return true
		}
	}
	return false
}

func isLetterOrDigit(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я', r == 'ё', r == 'Ё':
		return true
	}
	return false
}
