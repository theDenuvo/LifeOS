package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	appskills "github.com/theDenuvo/LifeOS/internal/application/skills"
	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

func setupSkillTools(t *testing.T) (*Registry, *appskills.Service) {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	clock := skillsTestClock{at: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	service := appskills.NewService(sqlite.NewSkillUnitOfWork(db), clock)
	registry := NewRegistry()
	RegisterSkillTools(registry, service)
	return registry, service
}

type skillsTestClock struct{ at time.Time }

func (c skillsTestClock) Now() time.Time { return c.at }

func TestSkillToolsCreateDraftAndPropose(t *testing.T) {
	registry, service := setupSkillTools(t)
	ctx := context.Background()

	// create_skill produces a draft that is not injected.
	created, err := registry.Execute(ctx, "create_skill", json.RawMessage(`{
		"name": "Подготовка к встрече",
		"purpose": "Готовить повестку и материалы к встрече заранее.",
		"when_to_use": "Когда до встречи меньше дня и пользователь просит подготовиться.",
		"instructions": "1. Найди встречу в календаре.\n2. Собери задачи и документы по теме.\n3. Составь повестку.\n4. Проверь, что каждый пункт повестки опирается на данные LifeOS.",
		"limitations": "Не выдумывай участников и темы.",
		"examples": "«Подготовься к встрече завтра» — повестка по данным календаря.",
		"tags": ["встреча", "подготовка"]
	}`))
	if err != nil {
		t.Fatalf("create_skill: %v", err)
	}
	result, ok := created.(map[string]any)
	if !ok || result["status"] != "draft" {
		t.Fatalf("create_skill result: %+v", created)
	}

	// Read tools see it; selection does not connect drafts.
	listed, err := registry.Execute(ctx, "list_skills", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_skills: %v", err)
	}
	if listed.(map[string]any)["total"] != 1 {
		t.Fatalf("list total: %+v", listed)
	}
	selected, err := service.SelectSkills(ctx, "подготовься к встрече завтра", nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("draft must not be injected: %+v", selected)
	}

	// Activate through the service, then propose an improvement: the
	// active text must not change until the user applies.
	skills, total, err := service.List(ctx, appskills.Filter{Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("list service: %v total %d", err, total)
	}
	id := skills[0].ID
	if _, err := service.ChangeStatus(ctx, id, skills[0].Version, domainskill.StatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	proposed, err := registry.Execute(ctx, "propose_skill_change", json.RawMessage(`{
		"skill_id": `+json.Number(json.RawMessage{}).String()+`
	}`))
	_ = proposed
	_ = err
	// The direct call above is intentionally malformed; the real call
	// below carries the full payload.
	proposed, err = registry.Execute(ctx, "propose_skill_change", json.RawMessage(
		`{"skill_id": `+itoa(id)+`, "problem": "шаг проверки пропущен", "instructions": "1. Найди встречу.\n2. Собери материалы.\n3. Составь повестку.\n4. Проверь каждый пункт по данным.\n5. Назови проверенное пользователю.", "expected_effect": "проверяемый результат", "test_scenarios": ["подготовься к встрече завтра"]}`))
	if err != nil {
		t.Fatalf("propose_skill_change: %v", err)
	}
	if proposed.(map[string]any)["status"] != "proposed" {
		t.Fatalf("proposal status: %+v", proposed)
	}
	// Rate + analyze through tools.
	if _, err := registry.Execute(ctx, "rate_skill", json.RawMessage(fmt.Sprintf(`{"skill_id": %d, "rating": "bad", "comment": "пропустил проверку"}`, id))); err != nil {
		t.Fatalf("rate_skill: %v", err)
	}
	analyzed, err := registry.Execute(ctx, "analyze_skill_use", json.RawMessage(fmt.Sprintf(`{"skill_id": %d}`, id)))
	if err != nil {
		t.Fatalf("analyze_skill_use: %v", err)
	}
	if analyzed.(map[string]any)["total"] == 0 {
		t.Fatalf("analysis must find the bad rating: %+v", analyzed)
	}
}
