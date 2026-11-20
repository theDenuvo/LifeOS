package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	appSettings "github.com/theDenuvo/LifeOS/internal/application/settings"
	appskills "github.com/theDenuvo/LifeOS/internal/application/skills"
	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

func skillTestContent() domainskill.Content {
	return domainskill.Content{
		Name:         "Встреча после поездки",
		Purpose:      "Готовить итоги поездки и следующий шаг по возвращении.",
		WhenToUse:    "Когда пользователь вернулся из поездки и просит подвести итоги.",
		Instructions: "1. Собери задачи и события за период поездки.\n2. Выдели незавершённое.\n3. Предложи следующий шаг и срок.\n4. Проверь, что каждый пункт опирается на данные LifeOS.",
		Limitations:  "Не выдумывай итоги, которых нет в данных.",
		Tags:         []string{"поездка", "итоги"},
	}
}

func TestSkillRepositoryRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	uow := NewSkillUnitOfWork(db)
	clock := appskillsClock{at: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	service := appskills.NewService(uow, clock)

	view, err := service.Create(context.Background(), appskills.CreateInput{Content: skillTestContent()}, domainskill.SourceUser)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.Skill.CurrentVersion != 1 || len(view.Versions) != 1 {
		t.Fatalf("version: %+v", view.Skill)
	}
	activated, err := service.ChangeStatus(context.Background(), view.Skill.ID, view.Skill.Version, domainskill.StatusActive)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if activated.Skill.Status != domainskill.StatusActive {
		t.Fatalf("status: %s", activated.Skill.Status)
	}
	// Feedback moves counters transactionally.
	if _, err := service.Rate(context.Background(), view.Skill.ID, domainskill.RatingBad, "пропустил шаг", nil); err != nil {
		t.Fatalf("rate: %v", err)
	}
	current, err := service.Get(context.Background(), view.Skill.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.Skill.BadCount != 1 {
		t.Fatalf("bad count: %d", current.Skill.BadCount)
	}
	// Proposal round-trip through the database.
	proposed := skillTestContent()
	proposed.Instructions += "\n5. Проверь итог."
	proposal, err := service.CreateProposal(context.Background(), appskills.ProposalInput{
		SkillID: &view.Skill.ID, Kind: domainskill.ProposalSkillVersion,
		Problem: "результат не проверялся", Evidence: []string{"плохая оценка"},
		Proposed: proposed, ExpectedEffect: "проверяемый результат",
		TestScenarios: []string{"примени навык Встреча после поездки"},
	})
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	applied, skill, err := service.ApplyProposal(context.Background(), proposal.ID, proposal.Version)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Status != domainskill.ProposalApplied || skill.CurrentVersion != 2 {
		t.Fatalf("applied: %+v v%d", applied, skill.CurrentVersion)
	}
	rolled, err := service.Rollback(context.Background(), view.Skill.ID, skill.Version, 1, "", domainskill.SourceUser)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.Skill.CurrentVersion != 3 {
		t.Fatalf("rollback version: %d", rolled.Skill.CurrentVersion)
	}
}

type appskillsClock struct{ at time.Time }

func (c appskillsClock) Now() time.Time { return c.at }

// A working-instructions proposal must apply on the real database:
// reading and writing app_settings inside the skill transaction shares
// its single connection (SetMaxOpenConns(1)). Going through
// settings.Service here would hand the second transaction to a
// connection that never returns and hang the HTTP request.
func TestWorkingProposalAppliesOnRealDB(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	uow := NewSkillUnitOfWork(db)
	clock := appskillsClock{at: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	settingsSvc := appSettings.NewService(NewSettingsUnitOfWork(db), clock)
	service := appskills.NewService(uow, clock, appskills.WithWorkingInstructions(testWorkingStore{settings: settingsSvc}))

	// Seed the working instructions block directly, as the settings
	// store would hold it.
	if err := uow.Do(context.Background(), func(_ context.Context, tx appskills.Transaction) error {
		raw, err := json.Marshal("Исходные инструкции.")
		if err != nil {
			return err
		}
		return tx.Settings().Put(context.Background(), appskills.WorkingInstructionsKey, raw, clock.Now())
	}); err != nil {
		t.Fatalf("seed working: %v", err)
	}
	proposal, err := service.CreateProposal(context.Background(), appskills.ProposalInput{
		Kind:           domainskill.ProposalWorkingInstructions,
		Problem:        "пропуски проверок",
		Evidence:       []string{"плохая оценка"},
		Proposed:       domainskill.Content{Instructions: "Улучшенные инструкции: проверяй каждый шаг."},
		ExpectedEffect: "проверки не пропускаются",
	})
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	if proposal.BaseWorkingHash == nil {
		t.Fatalf("proposal must carry the base fingerprint")
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := service.ApplyProposal(context.Background(), proposal.ID, proposal.Version)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("apply hung: working-instruction write still opens its own transaction")
	}
	text, err := service.GetWorkingInstructions(context.Background())
	_ = text
	if err != nil {
		// No WorkingInstructionsStore wired here; read the row directly.
		var stored string
		if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, appskills.WorkingInstructionsKey).Scan(&stored); err != nil {
			t.Fatalf("read stored: %v", err)
		}
		var decoded string
		if err := json.Unmarshal([]byte(stored), &decoded); err != nil {
			t.Fatalf("decode stored: %v", err)
		}
		if decoded != "Улучшенные инструкции: проверяй каждый шаг." {
			t.Fatalf("stored: %q", decoded)
		}
	} else if text != "Улучшенные инструкции: проверяй каждый шаг." {
		t.Fatalf("stored text: %q", text)
	}
}

type testWorkingStore struct {
	settings *appSettings.Service
}

func (s testWorkingStore) GetWorkingInstructions(ctx context.Context) (string, error) {
	raw, err := s.settings.Get(ctx, appskills.WorkingInstructionsKey)
	if err != nil {
		if errors.Is(err, appSettings.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	var text string
	return text, json.Unmarshal(raw, &text)
}

func (s testWorkingStore) SetWorkingInstructions(ctx context.Context, text string) error {
	raw, err := json.Marshal(text)
	if err != nil {
		return err
	}
	return s.settings.Put(ctx, appskills.WorkingInstructionsKey, raw)
}
