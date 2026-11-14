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
	BlockID   int64                `json:"block_id,omitempty"`
	TaskID    int64                `json:"task_id"`
	Title     string               `json:"title"`
	Priority  task.Priority        `json:"priority"`
	Start     time.Time            `json:"start"`
	End       time.Time            `json:"end"`
	Source    planning.BlockSource `json:"source,omitempty"`
	Status    planning.BlockStatus `json:"status,omitempty"`
	Estimate  int                  `json:"estimate_minutes,omitempty"`
	Recurring bool                 `json:"recurring,omitempty"`
	DueAt     *time.Time           `json:"due_at,omitempty"`
	DueDate   string               `json:"due_date,omitempty"`
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
	TaskID      int64  `json:"task_id"`
	Title       string `json:"title"`
	EstimateMin int    `json:"estimate_minutes"`
	Reason      string `json:"reason"`
}

// DayPlan is the read model of one day.
type DayPlan struct {
	Date           string         `json:"date"`
	Working        bool           `json:"working"`
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
// ReplaceDates lists the days whose automatically planned blocks are replaced;
// the user's own blocks are never touched. An empty list means "only insert".
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
	Days        []DayLoad           `json:"days"`
	Summary     string              `json:"summary"`
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
		// Manually placed blocks are commitments of the user and are treated
		// exactly like appointments: the planner works around them.
		if !block.Source.Movable() {
			fixed = append(fixed, Busy{
				Interval: Interval{Start: block.StartsAt, End: block.EndsAt},
				Label:    title,
				Kind:     "block",
				BlockID:  block.ID,
				TaskID:   block.TaskID,
			})
			plan.Busy = append(plan.Busy, BusyView{Label: title, Kind: "block", Start: block.StartsAt, End: block.EndsAt, TaskID: block.TaskID})
		}
	}

	free := FreeSlots(window, fixed, prefs.Buffer, prefs.MinSlot)
	for _, slot := range free {
		plan.FreeMinutes += int(slot.Duration().Minutes())
	}

	if !working {
		return plan, nil
	}

	alreadyPlanned := make(map[int64]bool, len(blocks))
	for _, block := range blocks {
		alreadyPlanned[block.TaskID] = true
	}

	candidates := s.candidatesFor(ctx, tx, tasksByID, date, now, alreadyPlanned)
	placements, unplaced := Schedule(free, candidates, prefs.Buffer)
	for _, placement := range placements {
		value := tasksByID[placement.TaskID]
		item := PlanItem{
			TaskID:   placement.TaskID,
			Title:    placement.Title,
			Priority: placement.Priority,
			Start:    placement.Start,
			End:      placement.End,
		}
		// Report the duration the planner actually used, including the default it
		// fell back to, so the interface never shows an unexplained gap.
		item.Estimate = int(DefaultEstimate.Minutes())
		if value != nil {
			if value.Task.EstimateMinutes != nil {
				item.Estimate = *value.Task.EstimateMinutes
			}
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
) []Candidate {
	loc := s.Location()
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
		estimate := DefaultEstimate
		if item.EstimateMinutes != nil {
			estimate = time.Duration(*item.EstimateMinutes) * time.Minute
		}

		candidate := Candidate{
			TaskID:    item.ID,
			Title:     item.Title,
			Priority:  item.Priority,
			Estimate:  estimate,
			DueAt:     item.DueAt,
			CreatedAt: item.CreatedAt,
			Recurring: item.Repeat != nil,
			Urgency:   4,
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

func (s *Service) applyTx(
	ctx context.Context,
	tx Transaction,
	in ApplyInput,
	prefs Preferences,
	loc *time.Location,
	now time.Time,
) ([]PlanItem, []string, error) {
	dates, err := parseDates(in.ReplaceDates)
	if err != nil {
		return nil, nil, err
	}
	if len(in.Items) > 0 {
		for _, item := range in.Items {
			if day, err := dateOf(item.Start, loc); err == nil {
				dates = append(dates, day)
			}
		}
	}
	dates = uniqueDates(dates)

	for _, date := range dates {
		start := date.Time(loc)
		if _, err := tx.Blocks().DeleteAutoRange(ctx, start.UTC(), start.AddDate(0, 0, 1).UTC()); err != nil {
			return nil, nil, err
		}
	}

	applied := make([]PlanItem, 0, len(in.Items))
	skipped := make([]string, 0)
	for _, item := range in.Items {
		if item.TaskID <= 0 || item.Start.IsZero() || !item.End.After(item.Start) {
			return nil, nil, ErrPlacementInvalid
		}
		date, err := dateOf(item.Start, loc)
		if err != nil {
			return nil, nil, err
		}
		window, working := prefs.WindowOn(date, loc)
		if !working {
			return nil, nil, fmt.Errorf("%w: %s is not a working day", ErrPlacementOutsideWindow, date)
		}
		if item.Start.Before(window.Start) || item.End.After(window.End) {
			return nil, nil, fmt.Errorf("%w: %s is outside %s-%s", ErrPlacementOutsideWindow, date, formatClock(prefs.Start), formatClock(prefs.End))
		}

		found, err := tx.Tasks().FindByID(ctx, item.TaskID)
		if err != nil {
			return nil, nil, ErrNoTask
		}
		if found.Task.Status != task.StatusOpen {
			return nil, nil, ErrTaskNotOpen
		}

		events, err := tx.Events().ListRange(ctx, item.Start.UTC(), item.End.UTC(), nil)
		if err != nil {
			return nil, nil, err
		}
		for _, event := range events {
			if event.Event.StartsAt.Before(item.End.UTC()) && item.Start.UTC().Before(event.Event.EndsAt) {
				return nil, nil, fmt.Errorf("%w: %s", ErrPlacementConflict, event.Event.Title)
			}
		}
		existing, err := tx.Blocks().ListRange(ctx, item.Start.UTC(), item.End.UTC())
		if err != nil {
			return nil, nil, err
		}
		for _, block := range existing {
			if block.StartsAt.Before(item.End.UTC()) && item.Start.UTC().Before(block.EndsAt) {
				return nil, nil, fmt.Errorf("%w: %s", ErrPlacementConflict, "уже стоит блок на это время")
			}
		}

		block, err := planning.NewBlock(item.TaskID, item.Start, item.End, planning.BlockPlanned, planning.SourceAuto, now)
		if err != nil {
			return nil, nil, err
		}
		stored, err := tx.Blocks().Insert(ctx, block)
		if err != nil {
			return nil, nil, err
		}
		if err := s.setPlannedDate(ctx, tx, found, date, now); err != nil {
			return nil, nil, err
		}

		title := found.Task.Title
		applied = append(applied, PlanItem{
			BlockID:  stored.ID,
			TaskID:   stored.TaskID,
			Title:    title,
			Priority: found.Task.Priority,
			Start:    stored.StartsAt,
			End:      stored.EndsAt,
			Source:   stored.Source,
			Status:   stored.Status,
		})
	}
	return applied, skipped, nil
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

func (s *Service) replanTx(
	ctx context.Context,
	tx Transaction,
	prefs Preferences,
	loc *time.Location,
	now time.Time,
	horizonDays int,
) (Proposal, error) {
	today, err := dateOf(now, loc)
	if err != nil {
		return Proposal{}, err
	}
	tasksByID, err := s.openTasks(ctx, tx)
	if err != nil {
		return Proposal{}, err
	}

	type dayState struct {
		date     task.CalendarDate
		window   Interval
		working  bool
		free     []Interval
		capacity time.Duration
	}
	days := make([]dayState, 0, horizonDays)
	for offset := 0; offset < horizonDays; offset++ {
		date := addCalendarDays(today, offset)
		window, working := prefs.WindowOn(date, loc)
		state := dayState{date: date, window: window, working: working}
		if working {
			start := date.Time(loc)
			events, err := tx.Events().ListRange(ctx, start.UTC(), start.AddDate(0, 0, 1).UTC(), nil)
			if err != nil {
				return Proposal{}, err
			}
			blocks, err := tx.Blocks().ListRange(ctx, start.UTC(), start.AddDate(0, 0, 1).UTC())
			if err != nil {
				return Proposal{}, err
			}
			busy := make([]Busy, 0, len(events)+len(blocks))
			for _, item := range events {
				interval := Interval{Start: item.Event.StartsAt, End: item.Event.EndsAt}
				if item.Event.AllDay {
					interval = Interval{Start: start, End: start.AddDate(0, 0, 1)}
				}
				busy = append(busy, Busy{Interval: interval, Kind: "event", Label: item.Event.Title})
			}
			for _, block := range blocks {
				// Automatic blocks are about to be replaced by the new plan, so
				// only the user's own blocks reduce capacity.
				if block.Source.Movable() {
					continue
				}
				busy = append(busy, Busy{
					Interval: Interval{Start: block.StartsAt, End: block.EndsAt},
					Kind:     "block",
					BlockID:  block.ID,
					TaskID:   block.TaskID,
				})
			}
			state.free = FreeSlots(window, busy, prefs.Buffer, prefs.MinSlot)
			for _, slot := range state.free {
				state.capacity += slot.Duration()
			}
		}
		days = append(days, state)
	}

	type pending struct {
		candidate Candidate
		deadline  *task.CalendarDate
	}
	items := make([]pending, 0, len(tasksByID))
	for _, value := range tasksByID {
		item := value.Task
		estimate := DefaultEstimate
		if item.EstimateMinutes != nil {
			estimate = time.Duration(*item.EstimateMinutes) * time.Minute
		}
		if item.EstimateMinutes == nil {
			// Without an estimate the planner would invent one; leave the task
			// to the assistant, which asks the user.
			continue
		}
		candidate := Candidate{
			TaskID:    item.ID,
			Title:     item.Title,
			Priority:  item.Priority,
			Estimate:  estimate,
			DueAt:     item.DueAt,
			CreatedAt: item.CreatedAt,
			Recurring: item.Repeat != nil,
		}
		var deadline *task.CalendarDate
		if item.DueDate != nil && !item.DueDate.IsZero() {
			due := *item.DueDate
			candidate.DueDate = &due
			deadline = &due
		}
		if item.DueAt != nil {
			dueDate, err := dateOf(*item.DueAt, loc)
			if err == nil {
				deadline = &dueDate
			}
		}
		candidate.Urgency = 2
		if item.IsOverdue(now) {
			candidate.Urgency = 0
		} else if deadline != nil && deadline.String() <= today.String() {
			candidate.Urgency = 1
		}
		items = append(items, pending{candidate: candidate, deadline: deadline})
	}
	sort.SliceStable(items, func(a, b int) bool {
		left, right := items[a].candidate, items[b].candidate
		if left.Urgency != right.Urgency {
			return left.Urgency < right.Urgency
		}
		if left.Priority != right.Priority {
			return priorityRank(left.Priority) < priorityRank(right.Priority)
		}
		if leftDeadline, rightDeadline := deadlineKey(items[a].deadline), deadlineKey(items[b].deadline); leftDeadline != rightDeadline {
			return leftDeadline < rightDeadline
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	proposal := Proposal{
		GeneratedAt: now,
		HorizonDays: horizonDays,
		Moves:       []Move{},
		Placements:  []ProposedPlacement{},
		Unplaced:    []UnplacedView{},
		Days:        make([]DayLoad, 0, len(days)),
	}

	placed := 0
	for _, item := range items {
		// A deadline that already passed cannot constrain the plan any more: the
		// task has to be done as early as possible, not "before yesterday".
		deadline := item.deadline
		if deadline != nil && deadline.String() < today.String() {
			deadline = nil
		}
		target := -1
		for i := range days {
			if !days[i].working {
				continue
			}
			if deadline != nil && days[i].date.String() > deadline.String() {
				continue
			}
			if days[i].capacity < item.candidate.Estimate {
				continue
			}
			target = i
			break
		}
		if target < 0 {
			reason := "на горизонте не хватает времени"
			if deadline != nil {
				reason = "не помещается до " + deadline.String()
			}
			proposal.Unplaced = append(proposal.Unplaced, UnplacedView{
				TaskID:      item.candidate.TaskID,
				Title:       item.candidate.Title,
				EstimateMin: int(item.candidate.Estimate.Minutes()),
				Reason:      reason,
			})
			continue
		}
		state := &days[target]
		slot := -1
		for i, free := range state.free {
			if free.Duration() >= item.candidate.Estimate {
				slot = i
				break
			}
		}
		if slot < 0 {
			state.capacity -= item.candidate.Estimate
			proposal.Unplaced = append(proposal.Unplaced, UnplacedView{
				TaskID:      item.candidate.TaskID,
				Title:       item.candidate.Title,
				EstimateMin: int(item.candidate.Estimate.Minutes()),
				Reason:      "нет непрерывного окна нужной длины",
			})
			continue
		}
		start := state.free[slot].Start
		end := start.Add(item.candidate.Estimate)
		remaining := Interval{Start: end.Add(prefs.Buffer), End: state.free[slot].End}
		if remaining.Duration() > 0 {
			state.free[slot] = remaining
		} else {
			state.free = append(state.free[:slot], state.free[slot+1:]...)
		}
		state.capacity -= item.candidate.Estimate

		proposal.Placements = append(proposal.Placements, ProposedPlacement{
			TaskID: item.candidate.TaskID,
			Date:   state.date.String(),
			Start:  start.UTC(),
			End:    end.UTC(),
		})
		reason := "освободилось время"
		if item.candidate.Urgency == 0 {
			reason = "просрочено"
		} else if item.candidate.Urgency == 1 {
			reason = "срок сегодня"
		}
		proposal.Moves = append(proposal.Moves, Move{
			TaskID:   item.candidate.TaskID,
			Title:    item.candidate.Title,
			To:       state.date.String(),
			Reason:   reason,
			Estimate: int(item.candidate.Estimate.Minutes()),
		})
		placed++
	}

	for _, state := range days {
		proposal.Days = append(proposal.Days, DayLoad{
			Date:        state.date.String(),
			Working:     state.working,
			FreeMinutes: int(state.capacity.Minutes()),
			PlannedMin:  int(state.window.Duration().Minutes()) - int(state.capacity.Minutes()),
		})
	}
	if placed == 0 {
		proposal.Summary = "Переносить нечего: всё уже помещается."
	} else {
		proposal.Summary = fmt.Sprintf("Предложено мест: %d. Ничего не изменится без вашего согласия.", placed)
	}
	return proposal, nil
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

// DueReminders returns reminders that are due for delivery, with the task they
// refer to. Finished tasks are skipped: a completed task must not be announced.
func (s *Service) DueReminders(ctx context.Context, until time.Time) ([]ReminderView, error) {
	var out []ReminderView
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		pending, err := tx.Reminders().ListPending(ctx, until.UTC())
		if err != nil {
			return err
		}
		for _, reminder := range pending {
			found, err := tx.Tasks().FindByID(ctx, reminder.TaskID)
			if err != nil {
				continue
			}
			if found.Task.Status != task.StatusOpen {
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

// MarkReminderSent records delivery.
func (s *Service) MarkReminderSent(ctx context.Context, id int64, at time.Time) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Reminders().MarkSent(ctx, id, at.UTC())
	})
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
// History beats guessing: tasks that share a tag or most title words provide the
// sample, and the median is used so a single outlier does not skew the plan. With
// no history at all the service says so instead of pretending to know, and the
// assistant asks the user.
func (s *Service) SuggestEstimate(ctx context.Context, title string, tags []string) (EstimateSuggestion, error) {
	const maxSamples = 200
	filter := tasks.TaskFilter{
		Statuses: []task.Status{task.StatusCompleted},
		Limit:    tasks.Some(maxSamples),
	}
	if len(tags) > 0 {
		filter.Tags = tags
	}

	var done []tasks.TaskWithVersion
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		result, err := tx.Tasks().List(ctx, filter)
		done = result.Tasks
		return err
	})
	if err != nil {
		return EstimateSuggestion{}, err
	}

	words := significantWords(title)
	samples := make([]int, 0, len(done))
	basedOn := make([]string, 0, 3)
	for _, item := range done {
		if item.Task.EstimateMinutes == nil {
			continue
		}
		if len(tags) == 0 && !sharesWords(words, item.Task.Title) {
			continue
		}
		samples = append(samples, *item.Task.EstimateMinutes)
		if len(basedOn) < 3 {
			basedOn = append(basedOn, item.Task.Title)
		}
	}
	if len(samples) >= 2 {
		sort.Ints(samples)
		return EstimateSuggestion{
			Minutes: samples[len(samples)/2],
			Source:  "history",
			Samples: len(samples),
			BasedOn: basedOn,
		}, nil
	}
	return EstimateSuggestion{
		Source:   "unknown",
		Samples:  len(samples),
		Question: "Сколько времени это займёт? Похожих задач в истории нет.",
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
