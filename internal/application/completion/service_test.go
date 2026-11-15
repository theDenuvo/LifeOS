package completion

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// fakeStore records checks in memory.
type fakeStore struct {
	reports  []Report
	decision map[int64]string
}

func (s *fakeStore) Save(ctx context.Context, report Report) (int64, error) {
	s.reports = append(s.reports, report)
	if s.decision == nil {
		s.decision = map[int64]string{}
	}
	return int64(len(s.reports)), nil
}

func (s *fakeStore) SetDecision(ctx context.Context, id int64, decision string, at time.Time) error {
	if s.decision == nil {
		s.decision = map[int64]string{}
	}
	for taskID := range s.decision {
		_ = taskID
	}
	s.decision[id] = decision
	return nil
}

func (s *fakeStore) Declined(ctx context.Context, taskID int64) (bool, error) {
	for _, decision := range s.decision {
		if decision == DecisionDeclined {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) Latest(ctx context.Context, taskID int64) (Report, bool, error) {
	for i := len(s.reports) - 1; i >= 0; i-- {
		if s.reports[i].TaskID == taskID {
			return s.reports[i], true, nil
		}
	}
	return Report{}, false, nil
}

// fakeTasks is a task store that records completions.
type fakeTasks struct {
	value     task.Task
	version   int
	completed int
	err       error
}

func (f *fakeTasks) GetTask(ctx context.Context, id int64) (tasks.TaskWithVersion, error) {
	if f.err != nil {
		return tasks.TaskWithVersion{}, f.err
	}
	return tasks.TaskWithVersion{Task: &f.value, Version: f.version}, nil
}

func (f *fakeTasks) CompleteTask(ctx context.Context, id int64, expectedVersion int, actor task.Actor) (tasks.TaskWithVersion, error) {
	f.completed++
	now := f.value.CreatedAt
	_ = f.value.Complete(now)
	return tasks.TaskWithVersion{Task: &f.value, Version: f.version + 1}, nil
}

// fakeSettings returns the auto-complete switch.
type fakeSettings struct{ enabled bool }

func (s fakeSettings) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if key != SettingAutoComplete {
		return nil, errors.New("setting not found")
	}
	return json.RawMessage(map[bool]string{true: "true", false: "false"}[s.enabled]), nil
}

// fakeSource returns prepared evidence.
type fakeSource struct {
	name      string
	available bool
	reason    string
	evidence  []Evidence
	err       error
}

func (s fakeSource) Name() string { return s.name }

func (s fakeSource) Status(ctx context.Context) SourceStatus {
	return SourceStatus{Name: s.name, Available: s.available, Reason: s.reason}
}

func (s fakeSource) Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error) {
	return s.evidence, s.err
}

// openTask builds a task with an expected result.
func openTask(t *testing.T, expected string) task.Task {
	t.Helper()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	created, err := task.NewTask("Отчёт для клиента", task.PriorityMedium, "Europe/Moscow", now)
	if err != nil {
		t.Fatalf("new task: %v", err)
	}
	created.ID = 42
	if expected != "" {
		if err := created.Update(now, task.WithExpectedResult(expected)); err != nil {
			t.Fatalf("set expected result: %v", err)
		}
	}
	return *created
}

func fixedClock() Clock {
	return fakeClock{now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

func TestCheckConfirmsStrongEvidence(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	store := &fakeStore{}
	service := NewService(&fakeTasks{value: value, version: 1}, []Source{
		fakeSource{name: "documents", available: true, evidence: []Evidence{{
			Source:     "documents",
			Reference:  "документ #7 «отчёт.md»",
			Snippet:    "Отчёт отправлен клиенту",
			At:         value.CreatedAt.Add(time.Hour),
			Confidence: 0.9,
		}}},
	}, WithStore(store), WithClock(fixedClock()))

	report, err := service.Check(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Verdict != VerdictConfirmed {
		t.Fatalf("verdict = %s, want confirmed", report.Verdict)
	}
	if !report.Confirms() {
		t.Fatalf("confidence = %v, want it above the threshold", report.Confidence)
	}
	if len(report.Evidence) != 1 || report.Evidence[0].Reference == "" {
		t.Fatalf("evidence was not kept: %+v", report.Evidence)
	}
	if len(store.reports) != 1 {
		t.Fatalf("the check was not recorded: %d", len(store.reports))
	}
}

func TestCheckReportsInsufficientEvidence(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	service := NewService(&fakeTasks{value: value, version: 1}, []Source{
		fakeSource{name: "calendar", available: true, evidence: []Evidence{{
			Source:     "calendar",
			Reference:  "событие «Отчёт для клиента»",
			At:         value.CreatedAt,
			Confidence: 0.4,
		}}},
	}, WithClock(fixedClock()))

	report, err := service.Check(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Verdict != VerdictInsufficient {
		t.Fatalf("verdict = %s, want insufficient", report.Verdict)
	}
	if report.Confirms() {
		t.Fatal("weak evidence must not confirm completion")
	}
}

func TestCheckReportsContradictorySources(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	service := NewService(&fakeTasks{value: value, version: 1}, []Source{
		fakeSource{name: "documents", available: true, evidence: []Evidence{
			{Source: "documents", Reference: "документ #7", Confidence: 0.9},
			{Source: "documents", Reference: "документ #8", Snippet: "отчёт не готово", Confidence: 0.85, Against: true},
		}},
	}, WithClock(fixedClock()))

	report, err := service.Check(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Verdict != VerdictContradictory {
		t.Fatalf("verdict = %s, want contradictory", report.Verdict)
	}
	if report.Confirms() {
		t.Fatal("a contradiction must never confirm completion")
	}
}

func TestCheckReportsUnavailableSources(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	service := NewService(&fakeTasks{value: value, version: 1}, []Source{
		UnavailableSource{SourceName: "github", Reason: "интеграция не настроена"},
		fakeSource{name: "documents", available: false, reason: "ошибка чтения"},
	}, WithClock(fixedClock()))

	report, err := service.Check(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.Sources) != 2 {
		t.Fatalf("sources = %+v, want both reported", report.Sources)
	}
	for _, source := range report.Sources {
		if source.Available {
			t.Errorf("source %q reported as available", source.Name)
		}
		if source.Reason == "" {
			t.Errorf("source %q has no explanation", source.Name)
		}
	}
	if report.Verdict != VerdictInsufficient {
		t.Fatalf("verdict = %s, want insufficient when nothing could be read", report.Verdict)
	}
}

func TestAutoCompleteRequiresTheSettingAndNoRefusal(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	evidence := []Source{fakeSource{name: "documents", available: true, evidence: []Evidence{{
		Source: "documents", Reference: "документ #7", Confidence: 0.9,
	}}}}

	// Off by default: a strong check still does not close the task.
	tasksOff := &fakeTasks{value: value, version: 1}
	service := NewService(tasksOff, evidence, WithSettings(fakeSettings{enabled: false}), WithClock(fixedClock()))
	report, completed, err := service.CompleteIfVerified(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if completed || tasksOff.completed != 0 {
		t.Fatal("the task was completed although the setting is off")
	}
	if report.Verdict != VerdictConfirmed {
		t.Fatalf("verdict = %s, want confirmed", report.Verdict)
	}

	// Enabled: the strong check closes it once.
	tasksOn := &fakeTasks{value: value, version: 1}
	service = NewService(tasksOn, evidence, WithSettings(fakeSettings{enabled: true}), WithClock(fixedClock()))
	_, completed, err = service.CompleteIfVerified(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !completed || tasksOn.completed != 1 {
		t.Fatalf("completed = %v (calls %d), want exactly one completion", completed, tasksOn.completed)
	}
}

func TestUserRefusalIsRemembered(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	store := &fakeStore{decision: map[int64]string{}}
	tasks := &fakeTasks{value: value, version: 1}
	service := NewService(tasks, []Source{
		fakeSource{name: "documents", available: true, evidence: []Evidence{{
			Source: "documents", Reference: "документ #7", Confidence: 0.9,
		}}},
	}, WithStore(store), WithSettings(fakeSettings{enabled: true}), WithClock(fixedClock()))

	if _, err := service.Decide(context.Background(), value.ID, DecisionDeclined); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if tasks.completed != 0 {
		t.Fatal("declining completed the task")
	}
	if _, completed, err := service.CompleteIfVerified(context.Background(), value.ID); err != nil {
		t.Fatalf("verify: %v", err)
	} else if completed {
		t.Fatal("a remembered refusal was overridden by the next check")
	}
}

func TestAcceptingTheCheckCompletesTheTask(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	tasks := &fakeTasks{value: value, version: 3}
	service := NewService(tasks, []Source{
		fakeSource{name: "documents", available: true, evidence: []Evidence{{
			Source: "documents", Reference: "документ #7", Confidence: 0.9,
		}}},
	}, WithStore(&fakeStore{}), WithClock(fixedClock()))

	report, err := service.Decide(context.Background(), value.ID, DecisionAccepted)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if report.Decision != DecisionAccepted {
		t.Fatalf("decision = %q, want accepted", report.Decision)
	}
	if tasks.completed != 1 {
		t.Fatalf("completions = %d, want 1", tasks.completed)
	}
}

func TestCheckingACompletedTaskChangesNothing(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	now := value.CreatedAt.Add(2 * time.Hour)
	if err := value.Complete(now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	tasks := &fakeTasks{value: value, version: 2}
	service := NewService(tasks, []Source{
		fakeSource{name: "documents", available: true, evidence: []Evidence{{
			Source: "documents", Reference: "документ #7", Confidence: 0.9,
		}}},
	}, WithSettings(fakeSettings{enabled: true}), WithClock(fixedClock()))

	report, completed, err := service.CompleteIfVerified(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Verdict != VerdictAlreadyCompleted {
		t.Fatalf("verdict = %s, want already_completed", report.Verdict)
	}
	if completed || tasks.completed != 0 {
		t.Fatal("a finished task was completed a second time")
	}
}

func TestSourceFailureIsReportedNotIgnored(t *testing.T) {
	value := openTask(t, "Отчёт отправлен клиенту")
	service := NewService(&fakeTasks{value: value, version: 1}, []Source{
		fakeSource{name: "documents", available: true, err: errors.New("disk unavailable")},
	}, WithClock(fixedClock()))

	report, err := service.Check(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.Sources) != 1 || report.Sources[0].Available {
		t.Fatalf("sources = %+v, want the failure reported", report.Sources)
	}
	if report.Verdict != VerdictInsufficient {
		t.Fatalf("verdict = %s, want insufficient", report.Verdict)
	}
}
