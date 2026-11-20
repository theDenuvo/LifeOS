package skills

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// Analyze scans the evidence LifeOS really has — feedback judgements,
// the usage journal and version churn — and turns it into concrete,
// testable improvement findings. It invents nothing: a skill with no bad
// judgement, no frequent use and no churn produces no finding.
func (s *Service) Analyze(ctx context.Context, skillID int64) ([]Finding, error) {
	skills, err := s.skillsForAnalysis(ctx, skillID)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, skill := range skills {
		skillFindings, err := s.analyzeSkill(ctx, skill)
		if err != nil {
			return nil, err
		}
		findings = append(findings, skillFindings...)
	}
	working, err := s.workingFinding(ctx, skills)
	if err != nil {
		return nil, err
	}
	findings = append(findings, working...)
	return findings, nil
}

func (s *Service) skillsForAnalysis(ctx context.Context, skillID int64) ([]*domainskill.Skill, error) {
	if skillID > 0 {
		skill, err := s.getSkill(ctx, skillID)
		if err != nil {
			return nil, err
		}
		return []*domainskill.Skill{skill}, nil
	}
	var out []*domainskill.Skill
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, _, err := tx.Skills().ListSkills(ctx, Filter{Limit: maxLimit})
		if err != nil {
			return err
		}
		for _, skill := range found {
			if skill.Status != domainskill.StatusArchived {
				out = append(out, skill)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) analyzeSkill(ctx context.Context, skill *domainskill.Skill) ([]Finding, error) {
	feedback, err := s.ListFeedback(ctx, skill.ID, 50)
	if err != nil {
		return nil, err
	}
	versions, err := s.listVersions(ctx, skill.ID)
	if err != nil {
		return nil, err
	}
	var bad []string
	good := 0
	for _, item := range feedback {
		if item.Rating == domainskill.RatingBad {
			comment := strings.TrimSpace(item.Comment)
			if comment == "" {
				comment = "оценка без комментария"
			}
			bad = append(bad, comment)
		} else {
			good++
		}
	}
	current := skill.Content()
	scenarios := testScenarios(skill)

	var findings []Finding
	// Rule 1: bad judgements — the strongest evidence there is.
	if len(bad) > 0 {
		evidence := append([]string{fmt.Sprintf("плохих оценок: %d (хороших: %d)", len(bad), good)}, bad...)
		proposed := current
		proposed.Instructions = strings.TrimSpace(current.Instructions) + "\n\n## Проверка результата\n- Перед завершением сверь каждый шаг с условиями навыка и данными LifeOS.\n- Если шаг не подтверждён данными или инструментом, остановись и уточни у пользователя, а не додумывай.\n- В конце коротко назови, что именно проверено."
		if strings.TrimSpace(current.Limitations) == "" {
			proposed.Limitations = "Не применяй навык, если условия из раздела «Когда применять» не выполнены полностью."
		} else if !strings.Contains(current.Limitations, "Не додумывай") {
			proposed.Limitations = strings.TrimSpace(current.Limitations) + " Не додумывай недостающие данные: уточни у пользователя."
		}
		findings = append(findings, Finding{
			SkillID: &skill.ID, SkillName: skill.Name, Kind: domainskill.ProposalSkillVersion,
			Problem:        fmt.Sprintf("пользователь %d раз отметил, что навык «%s» сработал плохо", len(bad), skill.Name),
			Evidence:       evidence,
			Proposed:       proposed,
			ExpectedEffect: "меньше случаев, когда навык применяется без проверки результата",
			TestScenarios:  scenarios,
			TestResult:     runTestScenarios(current, proposed, scenarios),
		})
	}
	// Rule 2: frequent use without any judgement — the step that would
	// make the result checkable is missing.
	if skill.UseCount >= 3 && len(feedback) == 0 && len(bad) == 0 {
		proposed := current
		if !strings.Contains(current.Instructions, "Проверка результата") {
			proposed.Instructions = strings.TrimSpace(current.Instructions) + "\n\n## Проверка результата\n- В конце проверь результат по данным LifeOS и назови его пользователю одним предложением."
		}
		findings = append(findings, Finding{
			SkillID: &skill.ID, SkillName: skill.Name, Kind: domainskill.ProposalSkillVersion,
			Problem:        fmt.Sprintf("навык «%s» применялся %d раз, но пользователь ни разу не оценил результат — проверять нечего", skill.Name, skill.UseCount),
			Evidence:       []string{fmt.Sprintf("применений: %d, оценок: 0", skill.UseCount)},
			Proposed:       proposed,
			ExpectedEffect: "результат применения становится проверяемым и оцениваемым",
			TestScenarios:  scenarios,
			TestResult:     runTestScenarios(current, proposed, scenarios),
		})
	}
	// Rule 3: active but never used for a long time — its conditions are
	// probably too broad or too narrow to ever connect.
	if skill.Status == domainskill.StatusActive && skill.UseCount == 0 && s.clock.Now().Sub(skill.CreatedAt) > analysisMaxAge {
		proposed := current
		proposed.WhenToUse = strings.TrimSpace(current.WhenToUse) + " Уточнение: применяй только когда поручение явно относится к этой задаче; в остальных случаях навык не нужен."
		findings = append(findings, Finding{
			SkillID: &skill.ID, SkillName: skill.Name, Kind: domainskill.ProposalSkillVersion,
			Problem:        fmt.Sprintf("активный навык «%s» ни разу не применялся за всё время — условия применения, видимо, не находят поручений", skill.Name),
			Evidence:       []string{"применений: 0"},
			Proposed:       proposed,
			ExpectedEffect: "навык перестаёт висеть в каталоге без дела и не подключается случайно",
			TestScenarios:  scenarios,
			TestResult:     runTestScenarios(current, proposed, scenarios),
		})
	}
	// Rule 4: version churn — the instruction keeps being rewritten, so
	// its boundaries probably are not written down.
	if len(versions) >= 4 {
		proposed := current
		if !strings.Contains(current.Limitations, "Границы навыка") {
			limitations := strings.TrimSpace(current.Limitations)
			if limitations != "" {
				limitations += "\n"
			}
			proposed.Limitations = limitations + "Границы навыка: навык описывает только этот класс задач и не распространяется на соседние."
		}
		findings = append(findings, Finding{
			SkillID: &skill.ID, SkillName: skill.Name, Kind: domainskill.ProposalSkillVersion,
			Problem:        fmt.Sprintf("инструкция навыка «%s» менялась %d раз — границы навыка не зафиксированы", skill.Name, len(versions)),
			Evidence:       []string{fmt.Sprintf("версий: %d", len(versions))},
			Proposed:       proposed,
			ExpectedEffect: "меньше правок в будущем: границы записаны явно",
			TestScenarios:  scenarios,
			TestResult:     runTestScenarios(current, proposed, scenarios),
		})
	}
	return findings, nil
}

func (s *Service) workingFinding(ctx context.Context, skills []*domainskill.Skill) ([]Finding, error) {
	badTotal := int64(0)
	badNames := map[string]struct{}{}
	for _, skill := range skills {
		if skill.BadCount > 0 {
			badTotal += skill.BadCount
			badNames[skill.Name] = struct{}{}
		}
	}
	if badTotal == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(badNames))
	for name := range badNames {
		names = append(names, name)
	}
	var existing string
	if s.working != nil {
		// A read failure must not become an empty base: it would
		// produce a proposal built on a text LifeOS never saw and
		// disarm the stale-base check.
		text, err := s.working.GetWorkingInstructions(ctx)
		if err != nil {
			return nil, err
		}
		existing = strings.TrimSpace(text)
	}
	proposedText := existing
	addition := "Перед применением навыка сверяй его условия и ограничения из библиотеки; если условия не выполнены, навык не применяется."
	if existing == "" {
		proposedText = addition
	} else if !strings.Contains(existing, "Перед применением навыка") {
		proposedText = existing + "\n" + addition
	} else {
		return nil, nil
	}
	scenarios := []string{"примени навык из библиотеки", "проверь условия навыка перед работой"}
	h := workingHash(existing)
	return []Finding{{
		BaseWorkingHash: &h,
		SkillName:       "рабочие инструкции",
		Kind:            domainskill.ProposalWorkingInstructions,
		Problem:         fmt.Sprintf("ошибки навыков повторяются: плохих оценок %d (%s)", badTotal, strings.Join(names, ", ")),
		Evidence:        []string{fmt.Sprintf("плохих оценок навыков: %d", badTotal)},
		Proposed:        domainskill.Content{Instructions: proposedText},
		ExpectedEffect:  "навыки применяются только при выполненных условиях, без случайных подключений",
		TestScenarios:   scenarios,
		TestResult:      "рабочие инструкции: проверена допустимость текста; выбор навыков не меняется",
	}}, nil
}

// ProposeImprovements analyzes and stores one proposal per finding (at
// most three), so the user decides on concrete, tested changes instead of
// a vague "improve everything".
func (s *Service) ProposeImprovements(ctx context.Context, skillID int64, conversationID *int64) ([]*domainskill.Proposal, error) {
	findings, err := s.Analyze(ctx, skillID)
	if err != nil {
		return nil, err
	}
	var proposals []*domainskill.Proposal
	for _, finding := range findings {
		if len(proposals) >= 3 {
			break
		}
		proposal, err := s.CreateProposal(ctx, ProposalInput{
			SkillID: finding.SkillID, Kind: finding.Kind, Problem: finding.Problem,
			Evidence: finding.Evidence, Proposed: finding.Proposed,
			ExpectedEffect: finding.ExpectedEffect, TestScenarios: finding.TestScenarios,
			BaseWorkingHash: finding.BaseWorkingHash,
			ConversationID:  conversationID,
		})
		if err != nil {
			// A finding that no longer validates (for example, the skill
			// was edited since) is skipped, not fatal to the others.
			continue
		}
		proposals = append(proposals, proposal)
	}
	return proposals, nil
}

func (s *Service) listVersions(ctx context.Context, skillID int64) ([]domainskill.Version, error) {
	var out []domainskill.Version
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Skills().ListVersions(ctx, skillID)
		if err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// testScenarios builds the sample queries a proposal is checked against:
// the skill's own name, its first tag and its purpose. They are the
// requests the skill should keep connecting after the change.
func testScenarios(skill *domainskill.Skill) []string {
	var scenarios []string
	if strings.TrimSpace(skill.Name) != "" {
		scenarios = append(scenarios, "примени навык "+skill.Name)
	}
	if len(skill.Tags) > 0 {
		scenarios = append(scenarios, "помоги с задачей про "+skill.Tags[0])
	}
	if strings.TrimSpace(skill.Purpose) != "" {
		scenarios = append(scenarios, skill.Purpose)
	}
	return scenarios
}

var _ = time.Now
