package completion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/settings"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// SettingAutoComplete is the switch that allows finishing a task automatically
// once its expected result was confirmed by evidence. It is off by default: closing
// work on the user's behalf is a decision, not a convenience.
const SettingAutoComplete = "planning.auto_complete_verified"

// Decisions a user can record about a check.
const (
	DecisionNone     = "none"
	DecisionAccepted = "accepted"
	DecisionDeclined = "declined"
)

// Store persists checks and the user's decisions about them.
type Store interface {
	Save(ctx context.Context, report Report) (int64, error)
	SetDecision(ctx context.Context, id int64, decision string, at time.Time) error
	// Declined reports whether the user already refused automatic completion for
	// the task. A refusal must not be overridden by the next check.
	Declined(ctx context.Context, taskID int64) (bool, error)
	// Latest returns the most recent check of a task.
	Latest(ctx context.Context, taskID int64) (Report, bool, error)
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}

// Settings reads the auto-complete preference.
type Settings interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
}

// TaskStore is the slice of the task service the checker needs.
type TaskStore interface {
	GetTask(ctx context.Context, id int64) (tasks.TaskWithVersion, error)
	CompleteTask(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (tasks.TaskWithVersion, error)
}

// Service checks completion against the sources LifeOS can actually read.
type Service struct {
	tasks    TaskStore
	store    Store
	sources  []Source
	settings Settings
	clock    Clock
}

// ServiceOption customises a Service.
type ServiceOption func(*Service)

// WithStore attaches the check history.
func WithStore(store Store) ServiceOption {
	return func(s *Service) { s.store = store }
}

// WithSettings attaches the settings reader used for the auto-complete switch.
func WithSettings(settings Settings) ServiceOption {
	return func(s *Service) { s.settings = settings }
}

// WithClock overrides the clock, so checks are reproducible in tests.
func WithClock(clock Clock) ServiceOption {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// NewService builds the completion checker.
func NewService(taskService TaskStore, sources []Source, opts ...ServiceOption) *Service {
	service := &Service{tasks: taskService, sources: sources, clock: systemClock{}}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Check compares the expected result of a task with the available sources.
//
// It never changes the task: the report is a proposal to confirm, with the grounds
// kept so the conclusion can be reviewed later.
func (s *Service) Check(ctx context.Context, taskID int64) (Report, error) {
	found, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return Report{}, err
	}
	value := found.Task
	report := Report{
		TaskID:    value.ID,
		Title:     value.Title,
		Verdict:   VerdictInsufficient,
		Evidence:  []Evidence{},
		Sources:   []SourceStatus{},
		Decision:  DecisionNone,
		CheckedAt: s.clock.Now(),
	}
	if value.ExpectedResult != nil {
		report.Expected = *value.ExpectedResult
	}
	if value.Status != task.StatusOpen {
		report.Verdict = VerdictAlreadyCompleted
		report.Confidence = 1
		report.Reason = "задача уже завершена: повторная проверка ничего не меняет"
	}

	for _, source := range s.sources {
		status := source.Status(ctx)
		if !status.Available {
			report.Sources = append(report.Sources, status)
			continue
		}
		evidence, err := source.Evidence(ctx, value, report.Expected)
		if err != nil {
			report.Sources = append(report.Sources, SourceStatus{
				Name:      status.Name,
				Available: false,
				Reason:    "источник не ответил: " + err.Error(),
			})
			continue
		}
		report.Sources = append(report.Sources, status)
		report.Evidence = append(report.Evidence, evidence...)
	}

	if value.Status == task.StatusOpen {
		report.Verdict, report.Confidence, report.Reason = conclude(report.Evidence)
	}

	if s.store != nil {
		id, err := s.store.Save(ctx, report)
		if err != nil {
			return report, err
		}
		report.ID = id
	}
	return report, nil
}

// conclude turns observations into a verdict.
//
// The distinction that matters is between "found the stated result" and "found
// something related": only the first may confirm anything, and a contradiction
// outranks a weak confirmation.
func conclude(evidence []Evidence) (Verdict, float64, string) {
	strongest := 0.0
	against := 0.0
	weak := false
	for _, item := range evidence {
		if item.Against {
			if item.Confidence > against {
				against = item.Confidence
			}
			continue
		}
		if item.Confidence > strongest {
			strongest = item.Confidence
		}
		if item.Confidence < ConfirmedConfidence {
			weak = true
		}
	}
	switch {
	case against > 0:
		// A single contradiction is enough to refuse a conclusion: two sources
		// disagreeing means the state is unknown, and guessing is worse than asking.
		confidence := against
		if strongest > confidence {
			confidence = strongest
		}
		return VerdictContradictory, confidence, "источники противоречат друг другу: есть и подтверждение, и опровержение"
	case strongest >= ConfirmedConfidence:
		return VerdictConfirmed, strongest, "ожидаемый результат найден в доступном источнике"
	case weak:
		return VerdictInsufficient, strongest, "есть только косвенное подтверждение: этого мало, чтобы закрыть задачу"
	default:
		return VerdictInsufficient, 0, "подтверждений не найдено ни в одном доступном источнике"
	}
}

// Latest returns the most recent recorded check of a task.
func (s *Service) Latest(ctx context.Context, taskID int64) (Report, bool, error) {
	if s.store == nil {
		return Report{}, false, nil
	}
	return s.store.Latest(ctx, taskID)
}

// Decide records what the user wants done with the check.
//
// Accepting completes the task; declining keeps it open and is remembered, so a
// later check does not keep insisting.
func (s *Service) Decide(ctx context.Context, taskID int64, decision string) (Report, error) {
	if decision != DecisionAccepted && decision != DecisionDeclined {
		return Report{}, ErrInvalidDecision
	}
	report, err := s.Check(ctx, taskID)
	if err != nil {
		return Report{}, err
	}
	now := s.clock.Now()
	if s.store != nil && report.ID != 0 {
		if err := s.store.SetDecision(ctx, report.ID, decision, now); err != nil {
			return report, err
		}
	}
	report.Decision = decision
	if decision != DecisionAccepted || report.Verdict == VerdictAlreadyCompleted {
		return report, nil
	}
	found, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return report, err
	}
	if found.Task.Status != task.StatusOpen {
		return report, nil
	}
	if _, err := s.tasks.CompleteTask(ctx, taskID, found.Version, task.ActorUser); err != nil {
		return report, err
	}
	return report, nil
}

// CompleteIfVerified finishes a task when evidence is strong and the user allowed
// it. It returns whether the task was completed.
func (s *Service) CompleteIfVerified(ctx context.Context, taskID int64) (Report, bool, error) {
	report, err := s.Check(ctx, taskID)
	if err != nil {
		return Report{}, false, err
	}
	enabled, err := s.autoCompleteEnabled(ctx)
	if err != nil {
		return report, false, err
	}
	if !enabled || !report.Confirms() {
		return report, false, nil
	}
	if s.store != nil {
		declined, err := s.store.Declined(ctx, taskID)
		if err != nil {
			return report, false, err
		}
		if declined {
			return report, false, nil
		}
	}
	found, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return report, false, err
	}
	if found.Task.Status != task.StatusOpen {
		return report, false, nil
	}
	if _, err := s.tasks.CompleteTask(ctx, taskID, found.Version, task.ActorSystem); err != nil {
		return report, false, err
	}
	return report, true, nil
}

func (s *Service) autoCompleteEnabled(ctx context.Context) (bool, error) {
	if s.settings == nil {
		return false, nil
	}
	raw, err := s.settings.Get(ctx, SettingAutoComplete)
	if err != nil {
		// A missing setting is not an error: the default is "off".
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		return false, nil
	}
	return enabled, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, settings.ErrNotFound) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// ErrInvalidDecision is returned for an unknown decision.
var ErrInvalidDecision = errInvalidDecision{}

type errInvalidDecision struct{}

func (errInvalidDecision) Error() string { return "decision must be accepted or declined" }

// Is lets callers use errors.Is with the sentinel.
func (errInvalidDecision) Is(target error) bool { _, ok := target.(errInvalidDecision); return ok }
