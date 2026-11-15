package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// Roadmap stage 4: long-term goals and initiative management.
//
// The assistant drafts goals and plans, but the product rules keep the
// decisions with the user: a created goal waits in `proposed` until the user
// approves it (approve_goal is confirmation-gated), and significant changes to
// an approved plan go through propose_goal_change instead of silent edits.
// Milestone status moves and progress notes are factual and stay reversible,
// so they apply directly.

// goalService is the slice of the goal service the tools need.
type goalService = appgoals.Service

// registerGoalTools exposes goal management to the model. It is a no-op
// without a service: a deployment without goals keeps every other tool.
func registerGoalTools(registry *Registry, service *goalService) {
	if service == nil {
		return
	}

	registry.Register(Tool{
		Spec: assistantSpec("list_goals", "Список долгосрочных целей с прогрессом и сигналами отклонений. Используй, чтобы знать, куда пользователь движется.",
			objectSchema(map[string]any{
				"status": enumProp("proposed", "active", "paused", "achieved", "abandoned"),
				"search": stringProp("Поиск по названию"),
				"limit":  integerProp("Сколько целей вернуть (1-50, по умолчанию 20)"),
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
			filter := appgoals.Filter{Query: args.Search}
			if args.Status != "" {
				status := domaingoal.Status(args.Status)
				if !status.IsValid() {
					return nil, fmt.Errorf("неизвестный статус %q", args.Status)
				}
				filter.Status = &status
			}
			if args.Limit > 0 {
				filter.Limit = args.Limit
			}
			views, total, err := service.List(ctx, filter)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(views))
			for _, view := range views {
				items = append(items, goalSummaryView(view))
			}
			return map[string]any{"goals": items, "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("get_goal", "Получить цель целиком: план этапов, связи, журнал прогресса, предложения по изменению и вычисленный обзор.",
			objectSchema(map[string]any{
				"id": integerProp("ID цели"),
			}, "id")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.Get(ctx, args.ID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_goal", "Поставить долгосрочную цель: суть, желаемый результат и план по этапам. Цель создаётся в статусе «предложено» и начинает выполняться только после утверждения пользователем.",
			objectSchema(map[string]any{
				"title":       stringProp("Короткое название цели"),
				"description": stringProp("Что означает достижение цели и как это проверить"),
				"target_date": stringProp("Желаемый срок, YYYY-MM-DD (необязательно)"),
				"milestones": map[string]any{
					"type": "array",
					"items": objectSchema(map[string]any{
						"title":    stringProp("Название этапа"),
						"criteria": stringProp("Чем измеряется выполнение этапа"),
						"due_date": stringProp("Срок этапа, YYYY-MM-DD (необязательно)"),
					}, "title"),
					"description": "Этапы плана; цель без плана тоже допустима",
				},
			}, "title")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				TargetDate  string   `json:"target_date"`
				Milestones  []goalMilestoneArg `json:"milestones"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input, err := buildCreateInput(args.Title, args.Description, args.TargetDate, args.Milestones)
			if err != nil {
				return nil, err
			}
			view, err := service.Create(ctx, input)
			if err != nil {
				return nil, goalToolError(err)
			}
			result := goalView(view)
			result["note"] = "Цель ждёт утверждения пользователя; работать по ней до утверждения нельзя"
			return result, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_goal_plan", "Изменить план цели, которая ещё не утверждена: название, описание, срок, добавить или убрать этапы. Для утверждённой цели используй propose_goal_change.",
			objectSchema(map[string]any{
				"goal_id":              integerProp("ID цели"),
				"expected_version":     integerProp("Версия цели из get_goal"),
				"title":                stringProp("Новое название"),
				"description":          stringProp("Новое описание"),
				"target_date":          stringProp("Новый срок, YYYY-MM-DD"),
				"clear_target_date":    booleanProp("Убрать срок"),
				"add_milestones": map[string]any{
					"type":  "array",
					"items": objectSchema(map[string]any{"title": stringProp("Название этапа"), "criteria": stringProp("Чем измеряется выполнение"), "due_date": stringProp("Срок, YYYY-MM-DD")}, "title"),
				},
				"remove_milestone_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			}, "goal_id", "expected_version")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID          int64              `json:"goal_id"`
				ExpectedVersion int                `json:"expected_version"`
				Title           *string            `json:"title"`
				Description     *string            `json:"description"`
				TargetDate      string             `json:"target_date"`
				ClearTarget     bool               `json:"clear_target_date"`
				AddMilestones   []goalMilestoneArg `json:"add_milestones"`
				RemoveIDs       []int64            `json:"remove_milestone_ids"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := appgoals.UpdatePlanInput{Title: args.Title, Description: args.Description, ClearTarget: args.ClearTarget, RemoveIDs: args.RemoveIDs}
			if args.TargetDate != "" {
				target, err := parseGoalDate(args.TargetDate)
				if err != nil {
					return nil, err
				}
				input.TargetDate = target
			}
			for _, ms := range args.AddMilestones {
				input.AddMilestones = append(input.AddMilestones, domaingoal.MilestoneInput{
					Title: ms.Title, Criteria: ms.Criteria, DueDate: parseOptionalGoalDate(ms.DueDate),
				})
			}
			view, err := service.UpdatePlan(ctx, args.GoalID, args.ExpectedVersion, input)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_goal_milestones", "Отметить фактическое состояние этапов плана: начат, выполнен, отменён или снова в работе. Это данные о прогрессе, а не изменение плана.",
			objectSchema(map[string]any{
				"goal_id": integerProp("ID цели"),
				"updates": map[string]any{
					"type": "array",
					"items": objectSchema(map[string]any{
						"milestone_id":     integerProp("ID этапа"),
						"status":           enumProp("pending", "in_progress", "done", "skipped"),
						"expected_version": integerProp("Версия этапа из get_goal"),
					}, "milestone_id", "status"),
				},
			}, "goal_id", "updates")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID  int64 `json:"goal_id"`
				Updates []struct {
					MilestoneID     int64  `json:"milestone_id"`
					Status          string `json:"status"`
					ExpectedVersion int    `json:"expected_version"`
				} `json:"updates"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if len(args.Updates) == 0 {
				return nil, errors.New("updates пуст")
			}
			view, err := service.Get(ctx, args.GoalID)
			if err != nil {
				return nil, goalToolError(err)
			}
			for _, update := range args.Updates {
				status := domaingoal.MilestoneStatus(update.Status)
				if !status.IsValid() {
					return nil, fmt.Errorf("неизвестный статус этапа %q", update.Status)
				}
				version := update.ExpectedVersion
				if version == 0 {
					for _, m := range view.Milestones {
						if m.ID == update.MilestoneID {
							version = m.Version
						}
					}
				}
				if _, err := service.UpdateMilestone(ctx, args.GoalID, update.MilestoneID, version, appgoals.MilestoneUpdateInput{Status: &status}); err != nil {
					return nil, goalToolError(err)
				}
			}
			updated, err := service.Get(ctx, args.GoalID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(updated), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("propose_goal_change", "Предложить существенное изменение утверждённой цели: формулировка, срок или структура плана. Пользователь увидит предложение и сам решит, применять ли его.",
			objectSchema(map[string]any{
				"goal_id":              integerProp("ID цели"),
				"summary":              stringProp("Что меняется и почему, одним понятным абзацем"),
				"title":                stringProp("Новое название"),
				"description":          stringProp("Новое описание"),
				"target_date":          stringProp("Новый срок, YYYY-MM-DD"),
				"clear_target_date":    booleanProp("Убрать срок"),
				"add_milestones": map[string]any{
					"type":  "array",
					"items": objectSchema(map[string]any{"title": stringProp("Название этапа"), "criteria": stringProp("Чем измеряется выполнение"), "due_date": stringProp("Срок, YYYY-MM-DD")}, "title"),
				},
				"complete_milestone_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"skip_milestone_ids":     map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"reopen_milestone_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"remove_milestone_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			}, "goal_id", "summary")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID         int64              `json:"goal_id"`
				Summary        string             `json:"summary"`
				Title          *string            `json:"title"`
				Description    *string            `json:"description"`
				TargetDate     string             `json:"target_date"`
				ClearTarget    bool               `json:"clear_target_date"`
				AddMilestones  []goalMilestoneArg `json:"add_milestones"`
				CompleteIDs    []int64            `json:"complete_milestone_ids"`
				SkipIDs        []int64            `json:"skip_milestone_ids"`
				ReopenIDs      []int64            `json:"reopen_milestone_ids"`
				RemoveIDs      []int64            `json:"remove_milestone_ids"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			payload := domaingoal.ProposalPayload{
				Title: args.Title, Description: args.Description, ClearTargetDate: args.ClearTarget,
				CompleteMilestoneIDs: args.CompleteIDs, SkipMilestoneIDs: args.SkipIDs,
				ReopenMilestoneIDs: args.ReopenIDs, RemoveMilestoneIDs: args.RemoveIDs,
			}
			if args.TargetDate != "" {
				target, err := parseGoalDate(args.TargetDate)
				if err != nil {
					return nil, err
				}
				payload.TargetDate = target
			}
			for _, ms := range args.AddMilestones {
				payload.AddMilestones = append(payload.AddMilestones, domaingoal.MilestoneInput{
					Title: ms.Title, Criteria: ms.Criteria, DueDate: parseOptionalGoalDate(ms.DueDate),
				})
			}
			proposal, err := service.ProposeChange(ctx, args.GoalID, payload, args.Summary)
			if err != nil {
				return nil, goalToolError(err)
			}
			return map[string]any{
				"proposal": goalProposalView(proposal),
				"note":     "Предложение ждёт решения пользователя; план не изменён",
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("approve_goal", "Утвердить предложенную цель и начать работу по ней. Это решение пользователя: вызывай только после явного согласия в диалоге.",
			objectSchema(map[string]any{
				"goal_id": integerProp("ID цели"),
			}, "goal_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID int64 `json:"goal_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.Approve(ctx, args.GoalID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				GoalID int64 `json:"goal_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Утвердить цель #%d и перевести её в работу.", args.GoalID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("apply_goal_proposal", "Применить предложенное изменение плана цели. Это решение пользователя: вызывай только после явного согласия в диалоге.",
			objectSchema(map[string]any{
				"goal_id":     integerProp("ID цели"),
				"proposal_id": integerProp("ID предложения"),
			}, "goal_id", "proposal_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID     int64 `json:"goal_id"`
				ProposalID int64 `json:"proposal_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.ApplyProposal(ctx, args.GoalID, args.ProposalID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				GoalID     int64 `json:"goal_id"`
				ProposalID int64 `json:"proposal_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Применить изменение плана цели #%d (предложение #%d).", args.GoalID, args.ProposalID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("reject_goal_proposal", "Отклонить предложенное изменение плана; цель остаётся без изменений.",
			objectSchema(map[string]any{
				"goal_id":     integerProp("ID цели"),
				"proposal_id": integerProp("ID предложения"),
			}, "goal_id", "proposal_id")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID     int64 `json:"goal_id"`
				ProposalID int64 `json:"proposal_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.RejectProposal(ctx, args.GoalID, args.ProposalID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("change_goal_status", "Перевести цель между статусами: приостановить, возобновить, отметить достигнутой или отказаться от неё.",
			objectSchema(map[string]any{
				"goal_id": integerProp("ID цели"),
				"status":  enumProp("active", "paused", "achieved", "abandoned"),
			}, "goal_id", "status")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID int64  `json:"goal_id"`
				Status string `json:"status"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			status := domaingoal.Status(args.Status)
			if !status.IsValid() {
				return nil, fmt.Errorf("неизвестный статус %q", args.Status)
			}
			view, err := service.SetStatus(ctx, args.GoalID, status)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				GoalID int64  `json:"goal_id"`
				Status string `json:"status"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Изменить статус цели #%d на «%s».", args.GoalID, args.Status), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("link_goal", "Связать цель с объектом LifeOS: рабочим пространством, задачей, событием календаря, документом, записью памяти или входящим.",
			objectSchema(map[string]any{
				"goal_id":     integerProp("ID цели"),
				"target_type": enumProp("conversation", "task", "event", "document", "memory", "inbox"),
				"target_id":   integerProp("ID объекта"),
				"relation":    stringProp("Как связан, например «материалы» или «обсуждение»"),
			}, "goal_id", "target_type", "target_id", "relation")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID     int64  `json:"goal_id"`
				TargetType string `json:"target_type"`
				TargetID   int64  `json:"target_id"`
				Relation   string `json:"relation"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			targetType := domaingoal.TargetType(args.TargetType)
			if !targetType.IsValid() {
				return nil, fmt.Errorf("неизвестный тип объекта %q", args.TargetType)
			}
			view, err := service.AddLink(ctx, args.GoalID, appgoals.LinkInput{
				TargetType: targetType, TargetID: args.TargetID, Relation: args.Relation,
			})
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("unlink_goal", "Разорвать связь цели с объектом.",
			objectSchema(map[string]any{
				"goal_id": integerProp("ID цели"),
				"link_id": integerProp("ID связи из get_goal"),
			}, "goal_id", "link_id")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID int64 `json:"goal_id"`
				LinkID int64 `json:"link_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.RemoveLink(ctx, args.GoalID, args.LinkID)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("record_goal_progress", "Добавить факт о продвижении цели в журнал прогресса: что сделано и на основании чего.",
			objectSchema(map[string]any{
				"goal_id":     integerProp("ID цели"),
				"summary":     stringProp("Что фактически произошло"),
				"source_type": enumProp("conversation", "task", "event", "document", "memory", "inbox"),
				"source_id":   integerProp("ID источника, если указан тип"),
			}, "goal_id", "summary")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				GoalID     int64  `json:"goal_id"`
				Summary    string `json:"summary"`
				SourceType string `json:"source_type"`
				SourceID   int64  `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := appgoals.RecordInput{Summary: args.Summary}
			if strings.TrimSpace(args.SourceType) != "" {
				sourceType := domaingoal.TargetType(args.SourceType)
				if !sourceType.IsValid() {
					return nil, fmt.Errorf("неизвестный тип источника %q", args.SourceType)
				}
				input.SourceType = &sourceType
				input.SourceID = &args.SourceID
			}
			view, err := service.Record(ctx, args.GoalID, input)
			if err != nil {
				return nil, goalToolError(err)
			}
			return goalView(view), nil
		},
	})
}

// goalMilestoneArg is one plan step in tool arguments.
type goalMilestoneArg struct {
	Title    string `json:"title"`
	Criteria string `json:"criteria"`
	DueDate  string `json:"due_date"`
}

// buildCreateInput converts tool arguments into a service input.
func buildCreateInput(title, description, targetDate string, milestones []goalMilestoneArg) (appgoals.CreateInput, error) {
	input := appgoals.CreateInput{Title: title, Description: description}
	if strings.TrimSpace(targetDate) != "" {
		target, err := parseGoalDate(targetDate)
		if err != nil {
			return input, err
		}
		input.TargetDate = target
	}
	for _, ms := range milestones {
		input.Milestones = append(input.Milestones, domaingoal.MilestoneInput{
			Title: ms.Title, Criteria: ms.Criteria, DueDate: parseOptionalGoalDate(ms.DueDate),
		})
	}
	return input, nil
}

// parseGoalDate parses a user-supplied calendar date in UTC.
func parseGoalDate(value string) (*time.Time, error) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("дата должна быть в формате YYYY-MM-DD")
	}
	t := parsed.UTC()
	return &t, nil
}

// parseOptionalGoalDate tolerates an absent or empty date.
func parseOptionalGoalDate(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := parseGoalDate(value)
	if err != nil {
		return nil
	}
	return parsed
}

// goalToolError maps service errors to model-facing messages.
func goalToolError(err error) error {
	switch {
	case errors.Is(err, appgoals.ErrNotFound):
		return errors.New("цель не найдена")
	case errors.Is(err, appgoals.ErrMilestoneNotFound):
		return errors.New("этап не найден")
	case errors.Is(err, appgoals.ErrLinkNotFound):
		return errors.New("связь не найдена")
	case errors.Is(err, appgoals.ErrProposalNotFound):
		return errors.New("предложение не найдено")
	case errors.Is(err, appgoals.ErrVersionConflict):
		return errors.New("цель изменена кем-то ещё; обнови данные через get_goal и повтори")
	case errors.Is(err, appgoals.ErrInvalidTransition):
		return fmt.Errorf("переход статуса не разрешён: %v", err)
	case errors.Is(err, appgoals.ErrInvalidTarget):
		return errors.New("объект для связи не существует; проверь ID")
	case errors.Is(err, appgoals.ErrGoalNotProposed):
		return errors.New("план утверждённой цели меняется только через propose_goal_change")
	case errors.Is(err, appgoals.ErrProposalNotAllowed):
		return errors.New("предложения по плану имеет смысл создавать только для активной цели")
	case errors.Is(err, appgoals.ErrNoPendingProposal):
		return errors.New("предложение уже решено или не существует")
	case errors.Is(err, appgoals.ErrAlreadyProposed):
		return errors.New("у цели уже есть предложение, ожидающее решения пользователя")
	case errors.Is(err, appgoals.ErrEmptyPayload):
		return errors.New("предложение ничего не меняет; перечисли конкретные изменения")
	case errors.Is(err, appgoals.ErrMilestoneOutside):
		return errors.New("этап принадлежит другой цели")
	}
	return err
}

// goalSummaryView renders one goal for lists.
func goalSummaryView(view *appgoals.View) map[string]any {
	g := view.Goal
	item := map[string]any{
		"id":      g.ID,
		"title":   g.Title,
		"status":  string(g.Status),
		"version": g.Version,
	}
	if view.Review != nil {
		if percent := view.Review.Progress.Percent; percent != nil {
			item["progress_percent"] = *percent
		}
		if view.Review.Signals.Stalled {
			item["stalled"] = true
			item["stall_reason"] = view.Review.Signals.StallReason
		}
		if view.Review.Signals.PendingProposal {
			item["pending_proposal"] = true
		}
	}
	return item
}

// goalView renders the full aggregate for the model.
func goalView(view *appgoals.View) map[string]any {
	g := view.Goal
	result := goalSummaryView(view)
	result["description"] = g.Description
	if g.TargetDate != nil {
		result["target_date"] = g.TargetDate.Format("2006-01-02")
	}
	if g.ApprovedAt != nil {
		result["approved_at"] = g.ApprovedAt.Format(time.RFC3339)
	}
	milestones := make([]map[string]any, 0, len(view.Milestones))
	for _, m := range view.Milestones {
		item := map[string]any{
			"id":      m.ID,
			"title":   m.Title,
			"status":  string(m.Status),
			"version": m.Version,
		}
		if m.Criteria != "" {
			item["criteria"] = m.Criteria
		}
		if m.DueDate != nil {
			item["due_date"] = m.DueDate.Format("2006-01-02")
		}
		milestones = append(milestones, item)
	}
	result["milestones"] = milestones
	links := make([]map[string]any, 0, len(view.Links))
	for _, link := range view.Links {
		links = append(links, map[string]any{
			"id": link.ID, "target_type": string(link.TargetType),
			"target_id": link.TargetID, "relation": link.Relation,
		})
	}
	result["links"] = links
	proposals := make([]map[string]any, 0, len(view.Proposals))
	for _, proposal := range view.Proposals {
		item := goalProposalView(&proposal)
		item["applied"] = proposal.Status == domaingoal.ProposalApplied
		proposals = append(proposals, item)
	}
	result["proposals"] = proposals
	log := make([]map[string]any, 0, len(view.ProgressLog))
	for _, event := range view.ProgressLog {
		item := map[string]any{"kind": string(event.Kind), "summary": event.Summary, "recorded_at": event.RecordedAt.Format(time.RFC3339)}
		if event.SourceType != nil {
			item["source_type"] = string(*event.SourceType)
			item["source_id"] = *event.SourceID
		}
		log = append(log, item)
	}
	result["progress_log"] = log
	if view.Review != nil {
		review := map[string]any{
			"milestones_total": view.Review.Progress.MilestonesTotal,
			"milestones_done":  view.Review.Progress.MilestonesDone,
			"linked_tasks":     view.Review.Progress.LinkedTasks,
			"linked_tasks_done": view.Review.Progress.LinkedTasksDone,
		}
		if last := view.Review.Progress.LastActivityAt; last != nil {
			review["last_activity_at"] = last.Format(time.RFC3339)
		}
		signals := view.Review.Signals
		if signals.Stalled || signals.Overdue || signals.NoOpenWork || signals.PendingProposal {
			review["signals"] = map[string]any{
				"stalled": signals.Stalled, "stall_reason": signals.StallReason,
				"overdue": signals.Overdue, "no_open_work": signals.NoOpenWork,
				"pending_proposal": signals.PendingProposal,
			}
		}
		result["review"] = review
	}
	return result
}

// goalProposalView renders one plan-change proposal.
func goalProposalView(proposal *domaingoal.Proposal) map[string]any {
	item := map[string]any{
		"id":     proposal.ID,
		"status": string(proposal.Status),
		"summary": proposal.Summary,
	}
	if payload, err := json.Marshal(proposal.Payload); err == nil {
		var decoded map[string]any
		if json.Unmarshal(payload, &decoded) == nil && len(decoded) > 0 {
			item["changes"] = decoded
		}
	}
	return item
}
