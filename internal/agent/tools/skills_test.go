package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// The rollback confirmation must identify the skill being changed: the
// user confirms a rewrite of one specific instruction, not "version N"
// in the abstract.
func TestSkillToolsRollbackPreviewIdentifiesSkill(t *testing.T) {
	registry, service := setupSkillTools(t)
	ctx := context.Background()

	view, err := service.Create(ctx, appskills.CreateInput{Content: domainskill.Content{
		Name:         "Подготовка к встрече",
		Purpose:      "Готовить повестку и материалы к встрече заранее.",
		WhenToUse:    "Когда до встречи меньше дня и пользователь просит подготовиться.",
		Instructions: "1. Найди встречу в календаре. 2. Собери задачи и документы по теме. 3. Составь повестку. 4. Проверь каждый пункт.",

		Tags: []string{"встреча"},
	}}, domainskill.SourceUser)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated := view.Skill
	updated.Instructions += "\n5. Позвони участникам."
	if _, err := service.Update(ctx, view.Skill.ID, view.Skill.Version, appskills.UpdateInput{Instructions: &updated.Instructions, ChangeNote: "добавлен шаг"}, domainskill.SourceUser); err != nil {
		t.Fatalf("update: %v", err)
	}

	// The pinned optimistic version is the one from before the update.
	preview, err := registry.Preview("rollback_skill", json.RawMessage(`{"skill_id": `+itoa(view.Skill.ID)+`, "version_no": 1, "expected_version": `+itoa(int64(view.Skill.Version))+`}`))
	if err == nil {
		t.Fatalf("preview with a stale expected_version must fail")
	}
	fresh, err := service.Get(ctx, view.Skill.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	preview, err = registry.Preview("rollback_skill", json.RawMessage(`{"skill_id": `+itoa(view.Skill.ID)+`, "version_no": 1, "expected_version": `+itoa(int64(fresh.Skill.Version))+`}`))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	// Execute with a stale pinned version is refused without touching
	// the skill: only the version the user saw is rolled back from.
	staleArgs := json.RawMessage(`{"skill_id": ` + itoa(view.Skill.ID) + `, "version_no": 1, "expected_version": ` + itoa(int64(view.Skill.Version)) + `}`)
	if _, err := registry.Execute(ctx, "rollback_skill", staleArgs); err == nil {
		t.Fatalf("stale rollback must be refused")
	}
	if untouched, err := service.Get(ctx, view.Skill.ID); err == nil && untouched.Skill.CurrentVersion != 2 {
		t.Fatalf("stale rollback must not change the skill: %+v", untouched.Skill)
	}
	for _, want := range []string{"Подготовка к встрече", "id", "версия 2", "к версии 1"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview must say %q is %q: %s", want, preview, preview)
		}
	}

	// Unknown skill id fails the preview instead of asking confirmation
	// for a change whose target is unknown.
	if _, err := registry.Preview("rollback_skill", json.RawMessage(`{"skill_id": 999999, "version_no": 1, "expected_version": 1}`)); err == nil {
		t.Fatalf("preview of unknown skill must fail")
	}
}
