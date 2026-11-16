package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

func setupAutomationDB(t *testing.T) *AutomationUnitOfWork {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "lifeos.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return NewAutomationUnitOfWork(db)
}

func automationTrigger() domainautomation.Trigger {
	return domainautomation.Trigger{
		Kind: domainautomation.TriggerSchedule,
		Schedule: &domainautomation.Schedule{
			Frequency: domainautomation.FrequencyWeekly,
			Time:      "08:30",
			Weekdays:  []int{1, 3, 5},
		},
	}
}

func automationSteps() []domainautomation.Step {
	return []domainautomation.Step{
		{Name: "list_tasks", Arguments: json.RawMessage(`{"status":"open"}`), Description: "Открытые задачи"},
		{Name: "capture_inbox", Description: "Напоминание"},
	}
}

func TestAutomationRepositoryRoundTrip(t *testing.T) {
	uow := setupAutomationDB(t)
	ctx := context.Background()
	var id int64
	err := uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		var err error
		id, err = tx.Automations().Insert(ctx, &domainautomation.Automation{
			Title: "Утренняя сводка", Description: "Каждые ПН/СР/ПТ",
			Status: domainautomation.StatusProposed, Trigger: automationTrigger(),
			Steps:          automationSteps(),
			ExpectedResult: "Сводка во входящих", Consequences: "Создаёт входящие",
			CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC(),
			Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var read *domainautomation.Automation
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		var err error
		read, err = tx.Automations().Get(ctx, id)
		return err
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.Title != "Утренняя сводка" || read.Status != domainautomation.StatusProposed {
		t.Fatalf("round trip lost fields: %+v", read)
	}
	if read.Trigger.Schedule == nil || read.Trigger.Schedule.Time != "08:30" || len(read.Trigger.Schedule.Weekdays) != 3 {
		t.Fatalf("trigger lost: %+v", read.Trigger)
	}
	if len(read.Steps) != 2 || read.Steps[0].Name != "list_tasks" {
		t.Fatalf("steps lost: %+v", read.Steps)
	}

	// Conditional versioned write.
	updated := *read
	updated.Status = domainautomation.StatusActive
	now := time.Unix(1700000100, 0).UTC()
	approved := now
	updated.ApprovedAt = &approved
	updated.UpdatedAt = now
	next := time.Unix(1700064000, 0).UTC()
	updated.NextRunAt = &next
	if err := uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		_, err := tx.Automations().Update(ctx, id, read.Version, &updated)
		return err
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A stale version conflicts.
	if err := uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		_, err := tx.Automations().Update(ctx, id, read.Version, &updated)
		return err
	}); err != appautomation.ErrVersionConflict {
		t.Fatalf("stale version must conflict, got %v", err)
	}
}

func TestAutomationRunReservationAndJournal(t *testing.T) {
	uow := setupAutomationDB(t)
	ctx := context.Background()
	var id int64
	err := uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		var err error
		id, err = tx.Automations().Insert(ctx, &domainautomation.Automation{
			Title: "Раз в неделю", Status: domainautomation.StatusActive,
			Trigger: automationTrigger(), Steps: automationSteps(),
			CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC(),
			Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	started := time.Unix(1700000600, 0).UTC()
	var run *domainautomation.Run
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		var err error
		run, err = tx.Automations().StartRun(ctx, id, "schedule", started)
		return err
	})
	if err != nil || run == nil {
		t.Fatalf("start run: run=%v err=%v", run, err)
	}

	// A second reservation is refused while the run is in progress.
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		second, err := tx.Automations().StartRun(ctx, id, "manual", started)
		if err != nil {
			return err
		}
		if second != nil {
			t.Fatalf("second reservation must be nil, got run %d", second.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}

	finished := started.Add(time.Minute)
	run.Status = domainautomation.RunFailed
	run.Steps = []domainautomation.RunStep{
		{Name: "list_tasks", Status: domainautomation.StepSucceeded, Output: `{"total":3}`, DurationMS: 12},
		{Name: "capture_inbox", Status: domainautomation.StepSkipped, Output: "пропущено"},
	}
	run.Summary = "Остановлено"
	run.Error = "шаг 2"
	run.FinishedAt = &finished
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		return tx.Automations().FinishRun(ctx, run)
	})
	if err != nil {
		t.Fatalf("finish run: %v", err)
	}

	var read *domainautomation.Run
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		var err error
		read, err = tx.Automations().GetRun(ctx, id, run.ID)
		return err
	})
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if read.Status != domainautomation.RunFailed || len(read.Steps) != 2 || read.Error != "шаг 2" {
		t.Fatalf("journal lost: %+v", read)
	}
	if read.StartedAt.Unix() != started.Unix() || read.FinishedAt == nil || read.FinishedAt.Unix() != finished.Unix() {
		t.Fatalf("timestamps lost: started=%v finished=%v", read.StartedAt, read.FinishedAt)
	}

	// ScheduleDue finds the active schedule automation only.
	err = uow.Do(ctx, func(ctx context.Context, tx appautomation.Transaction) error {
		due, err := tx.Automations().ScheduleDue(ctx, time.Unix(1700200000, 0).UTC())
		if err != nil {
			return err
		}
		if len(due) != 1 || due[0].ID != id {
			t.Fatalf("due = %+v, want automation %d", due, id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("schedule due: %v", err)
	}
}
