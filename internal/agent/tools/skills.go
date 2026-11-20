package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appassistant "github.com/theDenuvo/LifeOS/internal/application/assistant"
	appskills "github.com/theDenuvo/LifeOS/internal/application/skills"
	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// Roadmap stage 8: skills and assistant self-improvement.
//
// The assistant drafts and proposes: create_skill always produces a
// draft, changes to an active skill go through propose_skill_change,
// and propose_skill_improvements only prepares proposals — none of them
// changes an active instruction. Activation, disabling, archiving,
// rollback and deletion wait for the user's confirmation
// (pending_action), because they change what the assistant does by
// default. Rating a skill is a fact, not a rewrite, so it applies
// immediately.

// skillService is the slice of the skills service the tools need.
type skillService = appskills.Service

// skillTools are the meta tools of this file. An automation must not run
// them: designing, activating or improving skills is a conversation with
// the user, not something a schedule should do on its own.
var skillTools = map[string]bool{
	"list_skills":                true,
	"get_skill":                  true,
	"create_skill":               true,
	"propose_skill_change":       true,
	"propose_skill_improvements": true,
	"activate_skill":             true,
	"disable_skill":              true,
	"archive_skill":              true,
	"rollback_skill":             true,
	"rate_skill":                 true,
	"analyze_skill_use":          true,
}

// RegisterSkillTools exposes the skill library to the model. Like the
// automation and research tools it is registered after the service
// exists.
func RegisterSkillTools(registry *Registry, service *skillService) {
	if service == nil {
		return
	}

	registry.Register(Tool{
		Spec: assistantSpec("list_skills", "Список навыков LifeOS с назначением и условиями применения. Используй, чтобы знать, какой навык подходит к поручению, и чтобы показать пользователю библиотеку.",
			objectSchema(map[string]any{
				"status": enumProp("draft", "active", "disabled", "archived"),
				"search": stringProp("Поиск по названию, назначению и условиям"),
				"limit":  integerProp("Сколько навыков вернуть (1-50, по умолчанию 20)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Status string `json:"status"`
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			filter := appskills.Filter{Query: args.Search}
			if args.Status != "" {
				status := domainskill.Status(args.Status)
				if !status.IsValid() {
					return nil, fmt.Errorf("неизвестный статус %q", args.Status)
				}
				filter.Status = &status
			}
			if args.Limit > 0 {
				filter.Limit = args.Limit
			}
			skills, total, err := service.List(ctx, filter)
			if err != nil {
				return nil, skillToolError(err)
			}
			items := make([]map[string]any, 0, len(skills))
			for _, skill := range skills {
				items = append(items, skillSummaryView(skill))
			}
			return map[string]any{"skills": items, "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("get_skill", "Получить навык целиком: инструкции, ограничения, примеры, версия, история версий и последние оценки. Вызывай, когда пользователь просит применить конкретный навык или когда нужно следовать его инструкциям точно.",
			objectSchema(map[string]any{
				"id":   integerProp("ID навыка"),
				"name": stringProp("Точное название навыка (если ID неизвестен)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			skill, err := resolveSkill(ctx, service, args.ID, args.Name)
			if err != nil {
				return nil, skillToolError(err)
			}
			view, err := service.Get(ctx, skill.ID)
			if err != nil {
				return nil, skillToolError(err)
			}
			// An explicit fetch is a use: later analysis reads it.
			if conv := appassistant.ToolContextFrom(ctx); conv.ConversationID > 0 {
				_ = service.RecordUsage(ctx, skill.ID, &conv.ConversationID, "get_skill", []string{"явный запрос"})
			}
			return skillView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_skill", "Создать навык по просьбе пользователя: полноценная инструкция с назначением, условиями применения, шагами, ограничениями и примерами. Навык создаётся черновиком и не применяется, пока пользователь его не активирует.",
			objectSchema(map[string]any{
				"name":         stringProp("Название навыка"),
				"purpose":      stringProp("Назначение: какой класс задач решает"),
				"when_to_use":  stringProp("Когда применять: условия, при которых навык подходит"),
				"instructions": stringProp("Инструкции: шаги решения (минимум 50 символов)"),
				"limitations":  stringProp("Ограничения: чего навык не делает"),
				"examples":     stringProp("Примеры запросов, для которых навык подходит"),
				"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Теги для поиска навыка"},
				"change_note":  stringProp("Пояснение, откуда взялся навык (необязательно)"),
			}, "name", "purpose", "when_to_use", "instructions")),
		// Confirmation-gated: creating even a draft is a decision the
		// pending-action preview shows to the user.
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Name         string   `json:"name"`
				Purpose      string   `json:"purpose"`
				WhenToUse    string   `json:"when_to_use"`
				Instructions string   `json:"instructions"`
				Limitations  string   `json:"limitations"`
				Examples     string   `json:"examples"`
				Tags         []string `json:"tags"`
				ChangeNote   string   `json:"change_note"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.Create(ctx, appskills.CreateInput{
				Content: domainskill.Content{
					Name: args.Name, Purpose: args.Purpose, WhenToUse: args.WhenToUse,
					Instructions: args.Instructions, Limitations: args.Limitations,
					Examples: args.Examples, Tags: args.Tags,
				},
				ChangeNote: args.ChangeNote,
			}, domainskill.SourceAssistant)
			if err != nil {
				return nil, skillToolError(err)
			}
			result := skillView(view)
			result["note"] = "Навык — черновик: он не применяется, пока пользователь не активирует его в библиотеке навыков"
			return result, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Name string `json:"name"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Создать навык-%s", strings.TrimSpace(args.Name)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("propose_skill_change", "Предложить изменение активного навыка: новая версия с объяснением проблемы, примерами, ожидаемым эффектом. Предложение не меняет навык, пока пользователь его не применит в библиотеке.",
			objectSchema(map[string]any{
				"skill_id":        integerProp("ID навыка"),
				"problem":         stringProp("Какая проблема найдена"),
				"evidence":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Примеры и основания"},
				"instructions":    stringProp("Новая инструкция целиком"),
				"purpose":         stringProp("Новое назначение (необязательно)"),
				"when_to_use":     stringProp("Новые условия применения (необязательно)"),
				"limitations":     stringProp("Новые ограничения (необязательно)"),
				"examples":        stringProp("Новые примеры (необязательно)"),
				"tags":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"expected_effect": stringProp("Ожидаемый эффект изменения"),
				"test_scenarios":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Запросы, на которых проверяется новый вариант"},
			}, "skill_id", "problem", "instructions", "expected_effect")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID        int64    `json:"skill_id"`
				Problem        string   `json:"problem"`
				Evidence       []string `json:"evidence"`
				Instructions   string   `json:"instructions"`
				Purpose        string   `json:"purpose"`
				WhenToUse      string   `json:"when_to_use"`
				Limitations    string   `json:"limitations"`
				Examples       string   `json:"examples"`
				Tags           []string `json:"tags"`
				ExpectedEffect string   `json:"expected_effect"`
				TestScenarios  []string `json:"test_scenarios"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.Get(ctx, args.SkillID)
			if err != nil {
				return nil, skillToolError(err)
			}
			proposed := view.Skill.Content()
			proposed.Instructions = args.Instructions
			if args.Purpose != "" {
				proposed.Purpose = args.Purpose
			}
			if args.WhenToUse != "" {
				proposed.WhenToUse = args.WhenToUse
			}
			if args.Limitations != "" {
				proposed.Limitations = args.Limitations
			}
			if args.Examples != "" {
				proposed.Examples = args.Examples
			}
			if len(args.Tags) > 0 {
				proposed.Tags = args.Tags
			}
			var conversationID *int64
			if conv := appassistant.ToolContextFrom(ctx); conv.ConversationID > 0 {
				conversationID = &conv.ConversationID
			}
			proposal, err := service.CreateProposal(ctx, appskills.ProposalInput{
				SkillID: &args.SkillID, Kind: domainskill.ProposalSkillVersion,
				Problem: args.Problem, Evidence: args.Evidence, Proposed: proposed,
				ExpectedEffect: args.ExpectedEffect, TestScenarios: args.TestScenarios,
				ConversationID: conversationID,
			})
			if err != nil {
				return nil, skillToolError(err)
			}
			result := proposalView(proposal)
			result["note"] = "Предложение ждёт решения пользователя в библиотеке навыков; активная инструкция не изменена"
			return result, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("analyze_skill_use", "Проанализировать работу навыков по истории: применения, оценки пользователя и ошибки. Возвращает обоснованные выводы с примерами; ничего не меняет.",
			objectSchema(map[string]any{
				"skill_id": integerProp("ID навыка; без него анализируются все навыки"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64 `json:"skill_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			findings, err := service.Analyze(ctx, args.SkillID)
			if err != nil {
				return nil, skillToolError(err)
			}
			items := make([]map[string]any, 0, len(findings))
			for _, finding := range findings {
				items = append(items, findingView(finding))
			}
			return map[string]any{"findings": items, "total": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("propose_skill_improvements", "По прямому запросу пользователя: проанализировать историю работы и подготовить проверяемые предложения по улучшению навыков и рабочих инструкций. Предложения не применяются без решения пользователя.",
			objectSchema(map[string]any{
				"skill_id": integerProp("ID навыка; без него анализируются все навыки"),
			})),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64 `json:"skill_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			var conversationID *int64
			if conv := appassistant.ToolContextFrom(ctx); conv.ConversationID > 0 {
				conversationID = &conv.ConversationID
			}
			proposals, err := service.ProposeImprovements(ctx, args.SkillID, conversationID)
			if err != nil {
				return nil, skillToolError(err)
			}
			items := make([]map[string]any, 0, len(proposals))
			for _, proposal := range proposals {
				items = append(items, proposalView(proposal))
			}
			return map[string]any{"proposals": items, "total": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("activate_skill", "Активировать навык: он начнёт подключаться к подходящим поручениям. Вызывай только после явного согласия пользователя.",
			objectSchema(map[string]any{"skill_id": integerProp("ID навыка")}, "skill_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64 `json:"skill_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return changeSkillStatus(ctx, service, args.SkillID, domainskill.StatusActive)
		},
		Preview: func(raw json.RawMessage) (string, error) {
			return skillPreview(raw, "Активировать навык")
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("disable_skill", "Отключить навык: он перестанет подключаться, но останется в библиотеке. Вызывай только после явного согласия пользователя.",
			objectSchema(map[string]any{"skill_id": integerProp("ID навыка")}, "skill_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64 `json:"skill_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return changeSkillStatus(ctx, service, args.SkillID, domainskill.StatusDisabled)
		},
		Preview: func(raw json.RawMessage) (string, error) {
			return skillPreview(raw, "Отключить навык")
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("archive_skill", "Отправить навык в архив: он перестанет применяться, история сохранится. Вызывай только после явного согласия пользователя.",
			objectSchema(map[string]any{"skill_id": integerProp("ID навыка")}, "skill_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64 `json:"skill_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return changeSkillStatus(ctx, service, args.SkillID, domainskill.StatusArchived)
		},
		Preview: func(raw json.RawMessage) (string, error) {
			return skillPreview(raw, "Отправить навык в архив")
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("rollback_skill", "Вернуть навык к предыдущей версии: старая инструкция становится новой версией, история не переписывается. Вызывай только после явного согласия пользователя.",
			objectSchema(map[string]any{
				"skill_id":         integerProp("ID навыка"),
				"version_no":       integerProp("Номер версии, к которой вернуться"),
				"expected_version": integerProp("optimistic_version из get_skill: версия, которую видел при подготовке отката"),
			}, "skill_id", "version_no", "expected_version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID         int64 `json:"skill_id"`
				VersionNo       int   `json:"version_no"`
				ExpectedVersion int   `json:"expected_version"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			// The confirmation pins the version the user saw. The
			// pinned version is used as-is: re-reading the current one
			// here would apply the rollback to a state the user never
			// confirmed (Rollback refuses a stale version with 409).
			rolled, err := service.Rollback(ctx, args.SkillID, args.ExpectedVersion, args.VersionNo, "", domainskill.SourceAssistant)
			if err != nil {
				return nil, skillToolError(err)
			}
			return skillView(rolled), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				SkillID         int64 `json:"skill_id"`
				VersionNo       int   `json:"version_no"`
				ExpectedVersion int   `json:"expected_version"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			// The confirmation must name the skill being changed: with
			// several skills a bare "вернуть к версии N" does not say
			// which instruction is rewritten. The pinned version must
			// still be the current one: a changed skill needs a new
			// confirmation, not a rollback of an unseen state.
			view, err := service.Get(context.Background(), args.SkillID)
			if err != nil {
				return "", skillToolError(err)
			}
			if args.ExpectedVersion != view.Skill.Version {
				return "", fmt.Errorf("навык «%s» изменился (текущая версия %d): подготовьте откат заново", view.Skill.Name, view.Skill.CurrentVersion)
			}
			return fmt.Sprintf("Вернуть навык «%s» (id %d, текущая версия %d) к версии %d",
				view.Skill.Name, view.Skill.ID, view.Skill.CurrentVersion, args.VersionNo), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("rate_skill", "Записать оценку пользователя: навык сработал хорошо или плохо, с комментарием. Оценка учитывается при будущем анализе, но сама по себе инструкцию не меняет.",
			objectSchema(map[string]any{
				"skill_id": integerProp("ID навыка"),
				"rating":   enumProp("good", "bad"),
				"comment":  stringProp("Комментарий пользователя (необязательно)"),
			}, "skill_id", "rating")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SkillID int64  `json:"skill_id"`
				Rating  string `json:"rating"`
				Comment string `json:"comment"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			var conversationID *int64
			if conv := appassistant.ToolContextFrom(ctx); conv.ConversationID > 0 {
				conversationID = &conv.ConversationID
			}
			feedback, err := service.Rate(ctx, args.SkillID, domainskill.Rating(args.Rating), args.Comment, conversationID)
			if err != nil {
				return nil, skillToolError(err)
			}
			return map[string]any{"feedback_id": feedback.ID, "skill_id": feedback.SkillID, "rating": string(feedback.Rating)}, nil
		},
	})
}

func resolveSkill(ctx context.Context, service *skillService, id int64, name string) (*domainskill.Skill, error) {
	if id > 0 {
		view, err := service.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return view.Skill, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("нужен id или название навыка")
	}
	skills, _, err := service.List(ctx, appskills.Filter{Query: name, Limit: 50})
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if domainskill.NormalizeName(skill.Name) == domainskill.NormalizeName(name) {
			return skill, nil
		}
	}
	return nil, appskills.ErrNotFound
}

func changeSkillStatus(ctx context.Context, service *skillService, id int64, to domainskill.Status) (any, error) {
	view, err := service.Get(ctx, id)
	if err != nil {
		return nil, skillToolError(err)
	}
	updated, err := service.ChangeStatus(ctx, id, view.Skill.Version, to)
	if err != nil {
		return nil, skillToolError(err)
	}
	return skillView(updated), nil
}

func skillPreview(raw json.RawMessage, verb string) (string, error) {
	var args struct {
		SkillID int64 `json:"skill_id"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s №%d", verb, args.SkillID), nil
}

func skillSummaryView(skill *domainskill.Skill) map[string]any {
	return map[string]any{
		"id": skill.ID, "name": skill.Name, "purpose": skill.Purpose,
		"when_to_use": skill.WhenToUse, "status": string(skill.Status),
		"version": skill.CurrentVersion, "tags": skill.Tags,
		"uses": skill.UseCount, "good": skill.GoodCount, "bad": skill.BadCount,
	}
}

func skillView(view *appskills.View) map[string]any {
	skill := view.Skill
	result := skillSummaryView(skill)
	result["instructions"] = skill.Instructions
	result["limitations"] = skill.Limitations
	result["examples"] = skill.Examples
	result["source"] = string(skill.Source)
	result["created_by_assistant"] = skill.CreatedByAssistant
	result["created_at"] = skill.CreatedAt.Format("2006-01-02T15:04:05Z")
	result["updated_at"] = skill.UpdatedAt.Format("2006-01-02T15:04:05Z")
	result["optimistic_version"] = skill.Version
	versions := make([]map[string]any, 0, len(view.Versions))
	for _, version := range view.Versions {
		versions = append(versions, map[string]any{
			"version_no": version.VersionNo, "change_note": version.ChangeNote,
			"created_by": string(version.CreatedBy), "created_at": version.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	result["versions"] = versions
	proposals := make([]map[string]any, 0, len(view.Proposals))
	for _, proposal := range view.Proposals {
		proposals = append(proposals, proposalView(proposal))
	}
	result["proposals"] = proposals
	feedback := make([]map[string]any, 0, len(view.Feedback))
	for _, item := range view.Feedback {
		feedback = append(feedback, map[string]any{"rating": string(item.Rating), "comment": item.Comment, "created_at": item.CreatedAt.Format("2006-01-02T15:04:05Z")})
	}
	result["feedback"] = feedback
	return result
}

func proposalView(proposal *domainskill.Proposal) map[string]any {
	return map[string]any{
		"id": proposal.ID, "skill_id": proposal.SkillID, "kind": string(proposal.Kind),
		"status": string(proposal.Status), "problem": proposal.Problem,
		"evidence": proposal.Evidence, "expected_effect": proposal.ExpectedEffect,
		"test_scenarios": proposal.TestScenarios, "test_result": proposal.TestResult,
		"proposed": map[string]any{
			"name": proposal.Proposed.Name, "purpose": proposal.Proposed.Purpose,
			"when_to_use": proposal.Proposed.WhenToUse, "instructions": proposal.Proposed.Instructions,
			"limitations": proposal.Proposed.Limitations, "examples": proposal.Proposed.Examples,
			"tags": proposal.Proposed.Tags,
		},
	}
}

func findingView(finding appskills.Finding) map[string]any {
	return map[string]any{
		"skill_id": finding.SkillID, "skill_name": finding.SkillName, "kind": string(finding.Kind),
		"problem": finding.Problem, "evidence": finding.Evidence,
		"expected_effect": finding.ExpectedEffect, "test_result": finding.TestResult,
		"test_scenarios": finding.TestScenarios,
	}
}

func skillToolError(err error) error {
	if errors.Is(err, appskills.ErrNotFound) || errors.Is(err, appskills.ErrProposalNotFound) || errors.Is(err, appskills.ErrVersionNotFound) {
		return fmt.Errorf("навык не найден: %w", err)
	}
	return err
}
