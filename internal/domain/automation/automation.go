// Package automation owns the model of user automations: a trigger, a
// scenario of LifeOS tool steps, the approval lifecycle and the state the
// runner needs to execute them on time.
//
// The product rule behind the lifecycle: the assistant may design a scenario,
// but it only starts executing after the user approves the automation, and an
// approved automation can be paused, changed or archived by the user at any
// time. An automation is deliberately not a reminder: it runs a sequence of
// real actions with real effects, which is why it carries an explicit
// expected result and a description of possible consequences.
package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is the lifecycle of an automation.
//
// proposed — a drafted scenario that still needs user approval; active —
// approved and executed by the runner; paused — deliberately stopped, it
// keeps its configuration and history; archived — retired, kept for the run
// journal but never executed again.
type Status string

const (
	StatusProposed Status = "proposed"
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusArchived Status = "archived"
)

// IsValid reports whether the status is known.
func (s Status) IsValid() bool {
	switch s {
	case StatusProposed, StatusActive, StatusPaused, StatusArchived:
		return true
	default:
		return false
	}
}

// transitions lists the lifecycle moves the product rules allow. Archived is
// terminal: history stays readable, but a retired scenario does not return to
// work — a new automation is a cleaner decision than resurrecting an old one.
var transitions = map[Status]map[Status]struct{}{
	StatusProposed: {
		StatusActive:   {}, // user approved the scenario
		StatusArchived: {},
	},
	StatusActive: {
		StatusPaused:   {},
		StatusArchived: {},
	},
	StatusPaused: {
		StatusActive:   {}, // resumed
		StatusArchived: {},
	},
}

// CanTransition reports whether the status change is allowed.
func CanTransition(from, to Status) bool {
	_, ok := transitions[from][to]
	return ok
}

// TriggerKind selects when an automation runs.
type TriggerKind string

const (
	// TriggerSchedule runs by time: daily, weekly or monthly at a local
	// wall-clock time.
	TriggerSchedule TriggerKind = "schedule"
	// TriggerEvent runs when a LifeOS event arrives, optionally filtered by
	// payload fields.
	TriggerEvent TriggerKind = "event"
	// TriggerCondition runs while a proactive insight remains present, with a
	// cooldown between runs. Conditions are observed, not invented: the
	// automation reacts to the same picture the user sees in the insights
	// panel.
	TriggerCondition TriggerKind = "condition"
)

// IsValid reports whether the trigger kind is known.
func (k TriggerKind) IsValid() bool {
	switch k {
	case TriggerSchedule, TriggerEvent, TriggerCondition:
		return true
	default:
		return false
	}
}

// Frequency is the cadence of a schedule trigger.
type Frequency string

const (
	FrequencyDaily   Frequency = "daily"
	FrequencyWeekly  Frequency = "weekly"
	FrequencyMonthly Frequency = "monthly"
)

// IsValid reports whether the cadence is known.
func (f Frequency) IsValid() bool {
	switch f {
	case FrequencyDaily, FrequencyWeekly, FrequencyMonthly:
		return true
	default:
		return false
	}
}

// Schedule is a time-based trigger in the user's local timezone.
type Schedule struct {
	Frequency Frequency `json:"frequency"`
	// Time is the local wall clock in HH:MM.
	Time string `json:"time"`
	// Weekdays lists the days a weekly automation runs, 0 = Sunday … 6 =
	// Saturday. Required for weekly, ignored otherwise.
	Weekdays []int `json:"weekdays,omitempty"`
	// DayOfMonth selects the day for a monthly automation, 1–31, clamped to
	// the month length. Zero means the last day of the month.
	DayOfMonth int `json:"day_of_month,omitempty"`
}

// EventTrigger fires on a LifeOS event.
type EventTrigger struct {
	// Name is the event type as published on the stream, e.g. task.completed.
	Name string `json:"name"`
	// Match optionally requires payload fields to equal the given values.
	// Comparison is by string representation, so numbers match "3" as well
	// as 3.
	Match map[string]string `json:"match,omitempty"`
}

// ConditionTrigger fires while a proactive insight is present.
type ConditionTrigger struct {
	// Insight is the insight key to watch, e.g. overdue.
	Insight string `json:"insight"`
	// CooldownMinutes is the minimum gap between two runs of the same
	// automation while the condition holds. Zero falls back to the service
	// default (six hours).
	CooldownMinutes int `json:"cooldown_minutes,omitempty"`
}

// Trigger describes when an automation runs. Exactly the arm of its kind is
// filled; anything else is a validation error.
type Trigger struct {
	Kind      TriggerKind       `json:"kind"`
	Schedule  *Schedule         `json:"schedule,omitempty"`
	Event     *EventTrigger     `json:"event,omitempty"`
	Condition *ConditionTrigger `json:"condition,omitempty"`
}

// Validate checks the trigger as a whole.
func (t Trigger) Validate() error {
	if !t.Kind.IsValid() {
		return fmt.Errorf("неизвестный тип триггера %q", t.Kind)
	}
	switch t.Kind {
	case TriggerSchedule:
		if t.Schedule == nil {
			return errors.New("для триггера по времени нужен сценарий расписания")
		}
		return t.Schedule.Validate()
	case TriggerEvent:
		if t.Event == nil {
			return errors.New("для триггера по событию нужно имя события")
		}
		return t.Event.Validate()
	case TriggerCondition:
		if t.Condition == nil {
			return errors.New("для триггера по условию нужно имя наблюдения")
		}
		return t.Condition.Validate()
	}
	return nil
}

// Validate checks a schedule trigger.
func (s Schedule) Validate() error {
	if !s.Frequency.IsValid() {
		return fmt.Errorf("неизвестная периодичность %q", s.Frequency)
	}
	if len(s.Time) != 5 || s.Time[2] != ':' {
		return errors.New("время должно быть в формате HH:MM")
	}
	var hour, minute int
	if _, err := fmt.Sscanf(s.Time, "%02d:%02d", &hour, &minute); err != nil {
		return errors.New("время должно быть в формате HH:MM")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return errors.New("время должно быть внутри суток")
	}
	if s.Frequency == FrequencyWeekly {
		if len(s.Weekdays) == 0 {
			return errors.New("для еженедельного сценария нужны дни недели")
		}
		seen := map[int]bool{}
		for _, day := range s.Weekdays {
			if day < 0 || day > 6 {
				return errors.New("день недели — число от 0 (воскресенье) до 6 (суббота)")
			}
			if seen[day] {
				return errors.New("дни недели повторяются")
			}
			seen[day] = true
		}
	}
	if s.Frequency == FrequencyMonthly && (s.DayOfMonth < 0 || s.DayOfMonth > 31) {
		return errors.New("день месяца — число от 1 до 31, либо 0 для последнего дня месяца")
	}
	return nil
}

// Validate checks an event trigger.
func (e EventTrigger) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("имя события обязательно")
	}
	if IsReservedRunEvent(e.Name) {
		return fmt.Errorf("событие %q — внутреннее событие жизненного цикла запусков автоматизаций и не может быть триггером: оно публикуется каждым запуском, включая отказ в запуске, и самоподдерживающаяся цепочка пропусков недопустима", e.Name)
	}
	return nil
}

// IsReservedRunEvent reports whether the event type belongs to the
// automation run lifecycle. These events are published by the service for
// every run — including refused and skipped ones — so letting user triggers
// listen to them would make the chain-depth guard loop against itself: a
// depth-refused run publishes its skip, the skip event re-triggers the same
// automation, the next refusal publishes again, forever.
func IsReservedRunEvent(name string) bool {
	switch name {
	case "automation.run.started", "automation.run.finished":
		return true
	}
	return false
}

// Validate checks a condition trigger.
func (c ConditionTrigger) Validate() error {
	if strings.TrimSpace(c.Insight) == "" {
		return errors.New("наблюдение обязательно")
	}
	if c.CooldownMinutes < 0 {
		return errors.New("кулдаун не может быть отрицательным")
	}
	return nil
}

// MatchesPayload reports whether an event payload satisfies the trigger.
// Every match entry must equal the corresponding payload field, compared by
// string representation; a missing field does not match. An empty match
// accepts any payload of the right event type.
func (e EventTrigger) MatchesPayload(payload map[string]any) bool {
	for key, want := range e.Match {
		got, ok := lookupPayload(payload, key)
		if !ok || fmt.Sprint(got) != want {
			return false
		}
	}
	return true
}

// lookupPayload resolves dot-separated keys inside nested payload objects.
func lookupPayload(payload map[string]any, key string) (any, bool) {
	if value, ok := payload[key]; ok {
		return value, true
	}
	parts := strings.Split(key, ".")
	current := any(payload)
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// Step is one action of the scenario: a LifeOS tool call with its arguments.
type Step struct {
	// Name is the LifeOS tool the step runs.
	Name string `json:"name"`
	// Arguments is the JSON object passed to the tool.
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Description explains, in user language, what this step does. The
	// scenario must stay readable without decoding arguments.
	Description string `json:"description,omitempty"`
}

// RunStepStatus is the outcome of one executed step.
type RunStepStatus string

const (
	StepSucceeded RunStepStatus = "succeeded"
	StepFailed    RunStepStatus = "failed"
	StepSkipped   RunStepStatus = "skipped"
)

// RunStatus is the lifecycle of one execution of an automation.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunSkipped   RunStatus = "skipped"
)

// Terminal reports whether the run finished and its record will not change.
func (s RunStatus) Terminal() bool {
	return s == RunSucceeded || s == RunFailed || s == RunSkipped
}

// RunStep is the recorded result of one step inside a run.
type RunStep struct {
	Name       string        `json:"name"`
	Status     RunStepStatus `json:"status"`
	Output     string        `json:"output,omitempty"`
	Error      string        `json:"error,omitempty"`
	DurationMS int64         `json:"duration_ms"`
}

// Run is one execution of an automation with its journal.
type Run struct {
	ID           int64
	AutomationID int64
	Trigger      string // schedule | event | condition | manual
	Status       RunStatus
	Steps        []RunStep
	Summary      string
	Error        string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

// Validate checks the recorded run.
func (r *Run) Validate() error {
	if r.AutomationID <= 0 {
		return errors.New("запуск должен ссылаться на автоматизацию")
	}
	switch r.Trigger {
	case "schedule", "event", "condition", "manual":
	default:
		return fmt.Errorf("неизвестный источник запуска %q", r.Trigger)
	}
	if r.Status == RunRunning {
		if r.FinishedAt != nil {
			return errors.New("незавершённый запуск не может иметь момент окончания")
		}
	} else if !r.Status.Terminal() {
		return fmt.Errorf("неизвестный статус запуска %q", r.Status)
	}
	return nil
}

// Automation is the aggregate: a named scenario with its trigger, steps and
// execution state.
type Automation struct {
	ID          int64
	Title       string
	Description string
	Status      Status
	Trigger     Trigger
	Steps       []Step
	// ExpectedResult tells the user what a run should produce.
	ExpectedResult string
	// Consequences names the effects and risks the user accepts by enabling
	// the automation. The assistant fills it when designing the scenario.
	Consequences string
	ApprovedAt   *time.Time
	LastRunAt    *time.Time
	// NextRunAt is the runner's durable bookmark for schedule triggers. A
	// nil value means "recompute": after a restart the runner derives the
	// next occurrence from LastRunAt or the approval moment.
	NextRunAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

// Validate checks the aggregate's invariants.
func (a *Automation) Validate() error {
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return errors.New("название обязательно")
	}
	if utf8.RuneCountInString(title) > 200 {
		return errors.New("название длиннее 200 символов")
	}
	if len(a.Steps) == 0 {
		return errors.New("сценарий без шагов не имеет смысла")
	}
	if len(a.Steps) > maxSteps {
		return fmt.Errorf("в сценарии не может быть больше %d шагов", maxSteps)
	}
	names := map[string]bool{}
	for i, step := range a.Steps {
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("шаг %d: имя инструмента обязательно", i+1)
		}
		if names[step.Name] {
			return fmt.Errorf("шаг %d: инструмент %q уже используется", i+1, step.Name)
		}
		names[step.Name] = true
		if len(step.Arguments) > 0 {
			var value any
			if err := json.Unmarshal(step.Arguments, &value); err != nil {
				return fmt.Errorf("шаг %d: аргументы не являются корректным JSON", i+1)
			}
		}
		if utf8.RuneCountInString(step.Description) > 500 {
			return fmt.Errorf("шаг %d: описание длиннее 500 символов", i+1)
		}
	}
	if err := a.Trigger.Validate(); err != nil {
		return err
	}
	return nil
}

// maxSteps bounds a scenario: an automation is a repeating process, not a
// free-form program.
const maxSteps = 20

// Active reports whether the runner is expected to execute the automation.
func (a *Automation) Active() bool {
	return a.Status == StatusActive
}

// ConditionCooldown returns the effective cooldown of a condition trigger in
// minutes.
func (a *Automation) ConditionCooldown(defaultMinutes int) int {
	if a.Trigger.Kind != TriggerCondition || a.Trigger.Condition == nil {
		return 0
	}
	if a.Trigger.Condition.CooldownMinutes > 0 {
		return a.Trigger.Condition.CooldownMinutes
	}
	return defaultMinutes
}

// NextRun computes the first scheduled occurrence strictly after `after`, in
// the given location. Event and condition triggers have no schedule and
// always return nil.
func NextRun(t Trigger, after time.Time, loc *time.Location) *time.Time {
	if t.Kind != TriggerSchedule || t.Schedule == nil {
		return nil
	}
	hour, minute, ok := parseClock(t.Schedule.Time)
	if !ok {
		return nil
	}
	var next *time.Time
	switch t.Schedule.Frequency {
	case FrequencyDaily:
		candidate := atLocal(after.In(loc), hour, minute)
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		next = &candidate
	case FrequencyWeekly:
		next = nextWeekly(after, loc, hour, minute, t.Schedule.Weekdays)
	case FrequencyMonthly:
		next = nextMonthly(after, loc, hour, minute, t.Schedule.DayOfMonth)
	}
	return next
}

func parseClock(value string) (hour, minute int, ok bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(value, "%02d:%02d", &hour, &minute); err != nil {
		return 0, 0, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// atLocal builds the local instant of `day` at the given wall clock.
func atLocal(day time.Time, hour, minute int) time.Time {
	local := day.In(day.Location())
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, local.Location())
}

func nextWeekly(after time.Time, loc *time.Location, hour, minute int, weekdays []int) *time.Time {
	wanted := map[int]bool{}
	for _, day := range weekdays {
		wanted[day] = true
	}
	candidate := after.In(loc)
	for i := 0; i < 8; i++ {
		probe := atLocal(candidate, hour, minute)
		if probe.After(after) && wanted[int(probe.Weekday())] {
			return &probe
		}
		candidate = candidate.AddDate(0, 0, 1)
	}
	return nil
}

func nextMonthly(after time.Time, loc *time.Location, hour, minute int, dayOfMonth int) *time.Time {
	candidate := after.In(loc)
	// Up to 13 months forward covers 31 January skipping short months.
	for i := 0; i < 13; i++ {
		day := dayOfMonth
		if day == 0 {
			// The last day of the month.
			day = time.Date(candidate.Year(), candidate.Month()+1, 0, 0, 0, 0, 0, loc).Day()
		} else if last := time.Date(candidate.Year(), candidate.Month()+1, 0, 0, 0, 0, 0, loc).Day(); day > last {
			// A day the month does not have is skipped, like a wall calendar
			// would: "31st" monthly means months that have a 31st.
			candidate = time.Date(candidate.Year(), candidate.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
			continue
		}
		probe := time.Date(candidate.Year(), candidate.Month(), day, hour, minute, 0, 0, loc)
		if probe.After(after) {
			return &probe
		}
		candidate = time.Date(candidate.Year(), candidate.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	}
	return nil
}
