// Roadmap stage 6: user automations.
//
// The service keeps the decisions where the product rules put them: the
// assistant may draft a scenario, but it executes only after the user
// approves it, and the user can pause, change or archive an approved
// automation at any time. Runs are journalled step by step and every change
// is published to the live stream.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
	"github.com/theDenuvo/LifeOS/internal/events"
)

const (
	defaultLimit        = 30
	maxLimit            = 100
	defaultConditionGap = 6 * time.Hour
	// outputLimit keeps one step's recorded output readable in the journal;
	// a full result still lives in the tool's own storage.
	outputLimit = 4000
)

// Service coordinates automation mutations, approval, scheduling and runs.
type Service struct {
	uow        UnitOfWork
	clock      Clock
	tools      ToolExecutor
	validator  StepValidator
	conditions ConditionWatcher
	broker     events.Publisher
	timezone   string
}

// Option customises the service.
type Option func(*Service)

// WithPublisher publishes automation changes to the event stream.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) { s.broker = publisher }
}

// WithTools attaches the executor that runs the scenario's steps.
func WithTools(executor ToolExecutor) Option {
	return func(s *Service) { s.tools = executor }
}

// WithValidator attaches the tool allowlist used when designing a scenario.
func WithValidator(validator StepValidator) Option {
	return func(s *Service) { s.validator = validator }
}

// WithConditions attaches the insight watcher used by condition triggers.
func WithConditions(watcher ConditionWatcher) Option {
	return func(s *Service) { s.conditions = watcher }
}

// WithTimezone sets the location schedule moments are computed in.
func WithTimezone(timezone string) Option {
	return func(s *Service) {
		if timezone != "" {
			s.timezone = timezone
		}
	}
}

// NewService builds the automation service.
func NewService(uow UnitOfWork, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	s := &Service{uow: uow, clock: clock, timezone: "UTC"}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// loc resolves the service timezone, falling back to UTC.
func (s *Service) loc() *time.Location {
	location, err := time.LoadLocation(s.timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

// validateSteps refuses scenarios with tools the runner would refuse, so a
// drafted automation never stores steps it cannot execute.
func (s *Service) validateSteps(steps []domainautomation.Step) error {
	if s.validator == nil {
		return nil
	}
	for i, step := range steps {
		allowed, reason := s.validator.AllowedForAutomation(step.Name)
		if !allowed {
			return fmt.Errorf("шаг %d (%s): %w: %s", i+1, step.Name, ErrStepNotAllowed, reason)
		}
	}
	return nil
}

// Create drafts a new automation. It always starts proposed: executing is a
// user decision, not a design decision.
func (s *Service) Create(ctx context.Context, input CreateInput) (*View, error) {
	now := s.clock.Now().UTC()
	if err := s.validateSteps(input.Steps); err != nil {
		return nil, err
	}
	automation := &domainautomation.Automation{
		Title:          strings.TrimSpace(input.Title),
		Description:    strings.TrimSpace(input.Description),
		Status:         domainautomation.StatusProposed,
		Trigger:        input.Trigger,
		Steps:          input.Steps,
		ExpectedResult: strings.TrimSpace(input.ExpectedResult),
		Consequences:   strings.TrimSpace(input.Consequences),
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if err := automation.Validate(); err != nil {
		return nil, err
	}
	var id int64
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Automations().Insert(ctx, automation)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("automation.created", map[string]any{"id": id, "title": automation.Title, "status": string(automation.Status)})
	return s.Get(ctx, id)
}

// Get returns the automation with its recent run journal.
func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	var view *View
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Automations()
		found, err := repo.Get(ctx, id)
		if err != nil {
			return err
		}
		runs, _, err := repo.ListRuns(ctx, id, 10, 0)
		if err != nil {
			return err
		}
		view = &View{Automation: found, Runs: runs}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// List returns a page of automations.
func (s *Service) List(ctx context.Context, filter Filter) ([]*View, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultLimit
	}
	if filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var (
		views = make([]*View, 0, filter.Limit)
		total int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, count, err := tx.Automations().List(ctx, filter)
		if err != nil {
			return err
		}
		total = count
		repo := tx.Automations()
		for _, item := range found {
			runs, _, err := repo.ListRuns(ctx, item.ID, 3, 0)
			if err != nil {
				return err
			}
			views = append(views, &View{Automation: item, Runs: runs})
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// Stats returns how many automations sit in each status.
func (s *Service) Stats(ctx context.Context) (map[domainautomation.Status]int, error) {
	stats := map[domainautomation.Status]int{}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		for _, status := range []domainautomation.Status{
			domainautomation.StatusProposed,
			domainautomation.StatusActive,
			domainautomation.StatusPaused,
			domainautomation.StatusArchived,
		} {
			_, total, err := tx.Automations().List(ctx, Filter{Status: &status, Limit: 1})
			if err != nil {
				return err
			}
			stats[status] = total
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// Update edits the configuration of an automation. The user may change an
// approved automation directly — the interface is the user's instrument; the
// version guard keeps concurrent edits honest.
func (s *Service) Update(ctx context.Context, id int64, expectedVersion int, input UpdateInput) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Automations()
		current, err := repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.Status == domainautomation.StatusArchived {
			return fmt.Errorf("%w: архивная автоматизация не изменяется", ErrInvalidTransition)
		}
		if current.Status == domainautomation.StatusActive {
			if input.Trigger == nil && input.Steps == nil {
				// Text edits alone keep the scenario running.
			} else {
				return fmt.Errorf("%w: активная автоматизация меняется после паузы", ErrInvalidTransition)
			}
		}
		next := *current
		if input.Title != nil {
			next.Title = strings.TrimSpace(*input.Title)
		}
		if input.Description != nil {
			next.Description = strings.TrimSpace(*input.Description)
		}
		if input.ExpectedResult != nil {
			next.ExpectedResult = strings.TrimSpace(*input.ExpectedResult)
		}
		if input.Consequences != nil {
			next.Consequences = strings.TrimSpace(*input.Consequences)
		}
		if input.Trigger != nil {
			next.Trigger = *input.Trigger
		}
		if input.Steps != nil {
			if len(input.Steps) == 0 {
				return fmt.Errorf("сценарий без шагов не имеет смысла")
			}
			if err := s.validateSteps(input.Steps); err != nil {
				return err
			}
			next.Steps = input.Steps
		}
		next.UpdatedAt = now
		if err := next.Validate(); err != nil {
			return err
		}
		// Editing the trigger of an approved automation invalidates the old
		// schedule bookmark: the next pass recomputes it.
		if input.Trigger != nil && next.Trigger.Kind == domainautomation.TriggerSchedule {
			next.NextRunAt = nil
		}
		_, err = repo.Update(ctx, id, expectedVersion, &next)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("automation.updated", map[string]any{"id": id})
	return s.Get(ctx, id)
}

// Approve starts organized execution: proposed → active, with the schedule
// bookmark computed from the approval moment.
func (s *Service) Approve(ctx context.Context, id int64) (*View, error) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Automations()
		current, err := repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if !domainautomation.CanTransition(current.Status, domainautomation.StatusActive) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, current.Status, domainautomation.StatusActive)
		}
		next := *current
		next.Status = domainautomation.StatusActive
		next.ApprovedAt = &now
		next.UpdatedAt = now
		if next.Trigger.Kind == domainautomation.TriggerSchedule {
			next.NextRunAt = domainautomation.NextRun(next.Trigger, now, s.loc())
		}
		_, err = repo.Update(ctx, id, current.Version, &next)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("automation.updated", map[string]any{"id": id, "status": "active"})
	return s.Get(ctx, id)
}

// SetStatus pauses, resumes or archives an automation.
func (s *Service) SetStatus(ctx context.Context, id int64, status domainautomation.Status) (*View, error) {
	if !status.IsValid() {
		return nil, fmt.Errorf("неизвестный статус %q", status)
	}
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Automations()
		current, err := repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if !domainautomation.CanTransition(current.Status, status) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, current.Status, status)
		}
		next := *current
		next.Status = status
		next.UpdatedAt = now
		if status == domainautomation.StatusActive {
			// Resume from now, not from the stale bookmark: a paused
			// automation should not burst through every missed moment.
			if next.Trigger.Kind == domainautomation.TriggerSchedule {
				next.NextRunAt = domainautomation.NextRun(next.Trigger, now, s.loc())
			}
		}
		_, err = repo.Update(ctx, id, current.Version, &next)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("automation.updated", map[string]any{"id": id, "status": string(status)})
	return s.Get(ctx, id)
}

// Delete removes the automation and its run journal.
func (s *Service) Delete(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Automations().Delete(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("automation.deleted", map[string]any{"id": id})
	return nil
}

// RunNow executes an approved automation once, on the user's request.
func (s *Service) RunNow(ctx context.Context, id int64) (*View, error) {
	now := s.clock.Now().UTC()
	view, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !view.Automation.Active() {
		return nil, fmt.Errorf("%w: статус %s", ErrNotExecutable, view.Automation.Status)
	}
	if _, err := s.execute(ctx, view.Automation, "manual", now); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Runs lists the journal of one automation.
func (s *Service) Runs(ctx context.Context, id int64, limit, offset int) ([]*domainautomation.Run, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	var (
		runs  []*domainautomation.Run
		total int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		runs, total, err = tx.Automations().ListRuns(ctx, id, limit, offset)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

// Run returns one journalled run.
func (s *Service) Run(ctx context.Context, automationID, runID int64) (*domainautomation.Run, error) {
	var run *domainautomation.Run
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		run, err = tx.Automations().GetRun(ctx, automationID, runID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

// Tick is one runner pass: it executes the schedule moments that have come
// and the condition automations whose insight is present and whose cooldown
// has elapsed.
func (s *Service) Tick(ctx context.Context, now time.Time) TickResult {
	result := TickResult{}
	due, err := s.dueSchedule(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("automation: schedule query failed", "error", err)
		}
		return result
	}
	result.OverdueCatchups = len(due)
	for _, item := range due {
		if _, err := s.execute(ctx, item, "schedule", now); err != nil {
			if errors.Is(err, ErrAlreadyRunning) {
				result.SkippedBusy++
				continue
			}
			slog.Error("automation: scheduled run failed to start", "id", item.ID, "error", err)
			continue
		}
		result.ScheduleRuns++
	}

	conditioned, err := s.conditionAutomations(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("automation: condition query failed", "error", err)
		}
		return result
	}
	if len(conditioned) > 0 && s.conditions != nil {
		keys, err := s.conditions.CurrentInsightKeys(ctx)
		if err != nil {
			slog.Warn("automation: insight check failed", "error", err)
			return result
		}
		for _, item := range conditioned {
			if _, present := keys[item.Trigger.Condition.Insight]; !present {
				continue
			}
			if gap := item.ConditionCooldown(int(defaultConditionGap / time.Minute)); gap > 0 {
				if item.LastRunAt != nil && now.Sub(*item.LastRunAt) < time.Duration(gap)*time.Minute {
					continue
				}
			}
			if _, err := s.execute(ctx, item, "condition", now); err != nil {
				if errors.Is(err, ErrAlreadyRunning) {
					result.SkippedBusy++
					continue
				}
				slog.Error("automation: condition run failed to start", "id", item.ID, "error", err)
				continue
			}
			result.ConditionRuns++
		}
	}
	return result
}

// Observe feeds one LifeOS event to the event-triggered automations
// synchronously, as a direct (depth-zero) cause. The runner uses
// MatchedEvents plus RunFromEvent to dispatch runs asynchronously with chain
// depth tracking.
func (s *Service) Observe(ctx context.Context, eventType string, payload map[string]any, now time.Time) ObserveResult {
	result := ObserveResult{}
	matched, err := s.MatchedEvents(ctx, eventType, payload)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("automation: event query failed", "error", err)
		}
		return result
	}
	result.Matched = len(matched)
	for _, item := range matched {
		result.Runs++
		if _, err := s.execute(ctx, item, "event", now); err != nil {
			result.Runs--
			if errors.Is(err, ErrAlreadyRunning) {
				s.recordSkipped(ctx, item.ID, "event", "предыдущий запуск ещё выполняется", now)
				continue
			}
			slog.Error("automation: event run failed to start", "id", item.ID, "event", eventType, "error", err)
		}
	}
	return result
}

// MatchedEvents returns the active event-triggered automations the event
// should run: trigger name matches and every payload filter entry matches.
func (s *Service) MatchedEvents(ctx context.Context, eventType string, payload map[string]any) ([]*domainautomation.Automation, error) {
	all, err := s.eventAutomations(ctx)
	if err != nil {
		return nil, err
	}
	matched := make([]*domainautomation.Automation, 0, len(all))
	for _, item := range all {
		if item.Trigger.Event != nil && item.Trigger.Event.Name == eventType {
			matched = append(matched, item)
		}
	}
	result := make([]*domainautomation.Automation, 0, len(matched))
	for _, item := range matched {
		if item.Trigger.Event == nil || !item.Trigger.Event.MatchesPayload(payload) {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

// RunFromEvent executes one event-matched automation with the chain-depth
// guard. A run deeper than maxChainDepth is refused and stays in the journal
// as skipped with the reason, so no cycle — including an automation reacting
// to its own effects — can sustain itself.
func (s *Service) RunFromEvent(ctx context.Context, id int64, depth int, now time.Time) error {
	if depth >= maxChainDepth {
		reason := fmt.Sprintf("превышена глубина цепочки автоматизаций (%d): событие, вероятно, вызвано работой самой автоматизации", maxChainDepth)
		s.recordSkipped(ctx, id, "event", reason, now)
		slog.Warn("automation: chain depth limit reached", "automation_id", id, "depth", depth)
		return nil
	}
	view, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !view.Automation.Active() {
		return fmt.Errorf("%w: статус %s", ErrNotExecutable, view.Automation.Status)
	}
	if _, err := s.execute(ctx, view.Automation, "event", now); err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			s.recordSkipped(ctx, id, "event", "предыдущий запуск ещё выполняется", now)
			return nil
		}
		return err
	}
	return nil
}

// recordSkipped writes a visible journal entry for a launch that never ran
// and announces it, so the open interface shows the refusal immediately.
func (s *Service) recordSkipped(ctx context.Context, automationID int64, trigger, reason string, now time.Time) {
	var runID int64
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		runID, err = tx.Automations().RecordSkipped(ctx, automationID, trigger, reason, now)
		return err
	})
	if err != nil {
		slog.Error("automation: skipped-run record failed", "automation_id", automationID, "error", err)
		return
	}
	s.publish("automation.run.finished", map[string]any{
		"automation_id": automationID, "run_id": runID,
		"status": string(domainautomation.RunSkipped), "error": reason,
	})
}

// RecoverStaleRuns releases reservations a crashed process left behind.
// LifeOS is single-process, so at startup every run still marked running is
// stale by definition: the process that held it is gone. They become failed
// runs with the reason, and the automation is free to run again — schedule
// moments recompute from the last known point and catch up as usual.
//
// The caller must run this before anything can start a new run (in main,
// before the HTTP server opens): a recovery that races a legitimate run
// would mark that run failed while its steps keep executing.
func (s *Service) RecoverStaleRuns(ctx context.Context) ([]*domainautomation.Run, error) {
	var recovered []*domainautomation.Run
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		recovered, err = tx.Automations().RecoverRunning(ctx, s.clock.Now().UTC())
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, run := range recovered {
		slog.Warn("automation: recovered a run left running by an earlier process",
			"run_id", run.ID, "automation_id", run.AutomationID)
		s.publish("automation.run.finished", map[string]any{
			"automation_id": run.AutomationID, "run_id": run.ID,
			"status": string(domainautomation.RunFailed), "error": run.Error,
		})
	}
	return recovered, nil
}

// dueSchedule loads the schedule automations whose moment has come. A
// missing bookmark counts as due: the pass recomputes it and, if the
// recomputed moment is already in the past, catches up exactly once.
func (s *Service) dueSchedule(ctx context.Context, now time.Time) ([]*domainautomation.Automation, error) {
	var due []*domainautomation.Automation
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Automations().ScheduleDue(ctx, now)
		if err != nil {
			return err
		}
		for _, item := range found {
			if item.Trigger.Kind != domainautomation.TriggerSchedule || item.Trigger.Schedule == nil {
				continue
			}
			if item.NextRunAt == nil {
				// A lost bookmark (approval, restart or a trigger edit) is
				// recovered from the last known point.
				anchor := now
				if item.LastRunAt != nil && item.LastRunAt.Before(now) {
					anchor = *item.LastRunAt
				} else if item.ApprovedAt != nil && item.ApprovedAt.Before(now) {
					anchor = *item.ApprovedAt
				}
				next := domainautomation.NextRun(item.Trigger, anchor, s.loc())
				if next == nil {
					continue
				}
				item.NextRunAt = next
				// Not yet due: the recomputed moment is in the future, so the
				// bookmark alone is written back.
				if next.After(now) {
					if _, err := tx.Automations().Update(ctx, item.ID, item.Version, item); err != nil {
						return err
					}
					continue
				}
			}
			due = append(due, item)
		}
		return nil
	})
	return due, err
}

func (s *Service) conditionAutomations(ctx context.Context) ([]*domainautomation.Automation, error) {
	var found []*domainautomation.Automation
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		items, err := tx.Automations().ListByTrigger(ctx, domainautomation.TriggerCondition)
		if err != nil {
			return err
		}
		found = items
		return nil
	})
	return found, err
}

// eventAutomations returns every active event-triggered automation. The
// caller narrows the list by event name and payload filters.
func (s *Service) eventAutomations(ctx context.Context) ([]*domainautomation.Automation, error) {
	var found []*domainautomation.Automation
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		items, err := tx.Automations().ListByTrigger(ctx, domainautomation.TriggerEvent)
		if err != nil {
			return err
		}
		found = items
		return nil
	})
	return found, err
}

// execute runs the scenario once and journals every step. The run record is
// created inside a reservation, so two passes never run the same automation
// at once; a failure of one step stops the scenario and records the remaining
// steps as skipped with the reason.
func (s *Service) execute(ctx context.Context, item *domainautomation.Automation, trigger string, now time.Time) (*domainautomation.Run, error) {
	if !item.Active() {
		return nil, fmt.Errorf("%w: статус %s", ErrNotExecutable, item.Status)
	}
	if s.tools == nil {
		return nil, fmt.Errorf("инструменты не подключены: сценарий нельзя выполнить")
	}
	var (
		run *domainautomation.Run
		// scenario is the configuration the reservation saw. Executing it —
		// not the possibly stale listing copy — is what makes a pause-and-edit
		// between the listing and the reservation win.
		scenario *domainautomation.Automation
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		// Re-read inside the transaction: the status may have changed between
		// the listing and this moment, and pausing must win over executing.
		current, err := tx.Automations().Get(ctx, item.ID)
		if err != nil {
			return err
		}
		if !current.Active() {
			return fmt.Errorf("%w: статус %s", ErrNotExecutable, current.Status)
		}
		created, err := tx.Automations().StartRun(ctx, item.ID, trigger, now)
		if err != nil {
			return err
		}
		if created == nil {
			return ErrAlreadyRunning
		}
		run = created
		scenario = current
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.publish("automation.run.started", map[string]any{
		"automation_id": item.ID, "run_id": run.ID, "trigger": trigger,
	})

	steps := make([]domainautomation.RunStep, 0, len(scenario.Steps))
	status := domainautomation.RunSucceeded
	var failure string
	for i, step := range scenario.Steps {
		if failure != "" {
			steps = append(steps, domainautomation.RunStep{
				Name: step.Name, Status: domainautomation.StepSkipped,
				Output: "пропущено: предыдущий шаг завершился ошибкой",
			})
			continue
		}
		started := time.Now()
		result, err := s.tools.Execute(ctx, step.Name, step.Arguments)
		duration := time.Since(started).Milliseconds()
		if err != nil {
			steps = append(steps, domainautomation.RunStep{
				Name: step.Name, Status: domainautomation.StepFailed,
				Error: truncate(err.Error(), outputLimit), DurationMS: duration,
			})
			failure = fmt.Sprintf("шаг %d (%s): %v", i+1, step.Name, err)
			status = domainautomation.RunFailed
			continue
		}
		steps = append(steps, domainautomation.RunStep{
			Name: step.Name, Status: domainautomation.StepSucceeded,
			Output: truncate(mustJSON(result), outputLimit), DurationMS: duration,
		})
	}

	run.Steps = steps
	run.Status = status
	run.Error = failure
	if failure != "" {
		run.Summary = fmt.Sprintf("Выполнение остановлено: %s", failure)
	} else {
		run.Summary = fmt.Sprintf("Все шаги выполнены (%d)", len(steps))
	}
	finished := s.clock.Now().UTC()
	run.FinishedAt = &finished

	// The journal write must survive the caller's cancellation: a user closing
	// the HTTP connection (or any other ctx cancellation) is not a reason to
	// leave the reservation running. The context keeps its values but drops
	// the cancellation signal from here on — and carries its own deadline, so
	// a hung write fails instead of holding shutdown open: the runner joins
	// this goroutine before the database closes, and without a bound here that
	// join could wait forever.
	detached, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), maxFinalizeTimeout)
	defer cancelFinish()
	err = s.uow.Do(detached, func(ctx context.Context, tx Transaction) error {
		if err := tx.Automations().FinishRun(ctx, run); err != nil {
			return err
		}
		// LastRunAt and the next schedule moment are written back in the same
		// transaction as the journal, so the runner never loses either.
		current, err := tx.Automations().Get(ctx, item.ID)
		if err != nil {
			return err
		}
		current.LastRunAt = &finished
		current.UpdatedAt = finished
		if current.Trigger.Kind == domainautomation.TriggerSchedule {
			current.NextRunAt = domainautomation.NextRun(current.Trigger, now, s.loc())
		}
		_, err = tx.Automations().Update(ctx, item.ID, current.Version, current)
		return err
	})
	if err != nil {
		// The steps did run; only the journal write failed. Without the finish
		// the run would hold the reservation until the next restart, so the
		// failed write is retried best-effort as a plain failure record — on
		// the same detached context — and the startup recovery is the second
		// line of defence.
		slog.Error("automation: run journal write failed", "automation_id", item.ID, "run_id", run.ID, "error", err)
		reason := "журнал запуска не записан: " + err.Error()
		fallbackCtx, cancelFallback := context.WithTimeout(context.WithoutCancel(ctx), maxFinalizeTimeout)
		defer cancelFallback()
		if fallbackErr := s.uow.Do(fallbackCtx, func(ctx context.Context, tx Transaction) error {
			return tx.Automations().FailRun(ctx, run.ID, reason, finished)
		}); fallbackErr != nil {
			slog.Error("automation: run reservation release failed", "automation_id", item.ID, "run_id", run.ID, "error", fallbackErr)
		} else {
			s.publish("automation.run.finished", map[string]any{
				"automation_id": item.ID, "run_id": run.ID,
				"status": string(domainautomation.RunFailed), "error": reason,
			})
		}
		return run, nil
	}

	s.publish("automation.run.finished", map[string]any{
		"automation_id": item.ID, "run_id": run.ID,
		"status": string(run.Status), "summary": run.Summary, "error": run.Error,
	})
	return run, nil
}

func (s *Service) publish(eventType string, payload map[string]any) {
	if s.broker == nil {
		return
	}
	if err := s.broker.Publish(eventType, payload); err != nil {
		// Durable state is already written; a lost live event only delays the
		// interface refresh.
		slog.Error("publish automation event failed", "event", eventType, "error", err)
	}
}

func mustJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
