package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

// Roadmap stage 6: user automations.
//
// The assistant designs a scenario — a trigger plus a sequence of LifeOS
// tool steps with an expected result and named consequences — but execution
// stays with the user's decision: the created automation waits in `proposed`
// until approve_automation is confirmed, and a manual run asks for
// confirmation because it has real effects right now.

// automationService is the slice of the automation service the tools need.
type automationService = appautomation.Service

// AutomationStepValidator adapts the registry to the automation service's
// StepValidator port: it decides which tools a scenario may contain.
func AutomationStepValidator(registry *Registry) appautomation.StepValidator {
	return automationToolCheck{registry: registry}
}

type automationToolCheck struct{ registry *Registry }

// AllowedForAutomation implements appautomation.StepValidator.
func (c automationToolCheck) AllowedForAutomation(name string) (bool, string) {
	if automationTools[name] {
		return false, "инструменты проектирования автоматизаций не запускаются по расписанию"
	}
	if workspaceOnlyTools[name] {
		return false, "инструменту нужен контекст диалога, которого у автоматизации нет"
	}
	if infrastructureTools[name] {
		return false, "изменения на машине выполняются только по отдельному согласованному плану"
	}
	if researchPermissionTools[name] {
		return false, "доступ к внешним источникам согласует пользователь, а не расписание"
	}
	if !c.registry.Has(name) {
		return false, "инструмент не зарегистрирован"
	}
	return true, ""
}

// automationTools are the meta tools of this file. An automation may not run
// them: designing or approving automations is a conversation with the user,
// not something a schedule should do on its own.
var automationTools = map[string]bool{
	"list_automations":   true,
	"get_automation":     true,
	"create_automation":  true,
	"update_automation":  true,
	"approve_automation": true,
	"pause_automation":   true,
	"resume_automation":  true,
	"archive_automation": true,
	"run_automation":     true,
}

// workspaceOnlyTools need a conversation context a scheduled run does not
// have: routing, delegation and reporting depend on a current dialogue.
var workspaceOnlyTools = map[string]bool{
	"list_workspaces":       true,
	"create_workspace":      true,
	"delegate_to_workspace": true,
	"report_result":         true,
}

// infrastructureTools change the machine or the LifeOS core. The product
// rules put them behind an explicit per-change plan and approval; an
// unattended automation is not that approval, so scenarios refuse them.
var infrastructureTools = map[string]bool{
	"run_command": true,
	"write_file":  true,
	"edit_file":   true,
	"delete_path": true,
}

// RegisterAutomationTools exposes automation management to the model.
//
// It is deliberately not part of NewLifeOSTools: the automation service owns
// the registry as its step executor, so it has to exist before the tools
// attach. main wires the service first and calls this afterwards.
func RegisterAutomationTools(registry *Registry, service *automationService) {
	if service == nil {
		return
	}

	registry.Register(Tool{
		Spec: assistantSpec("list_automations", "Список пользовательских автоматизаций с триггерами, статусами и последними запусками.",
			objectSchema(map[string]any{
				"status": enumProp("proposed", "active", "paused", "archived"),
				"search": stringProp("Поиск по названию и описанию"),
				"limit":  integerProp("Сколько автоматизаций вернуть (1-100, по умолчанию 30)"),
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
			filter := appautomation.Filter{Query: args.Search}
			if args.Status != "" {
				status := domainautomation.Status(args.Status)
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
				items = append(items, automationSummaryView(view))
			}
			return map[string]any{"automations": items, "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("get_automation", "Получить автоматизацию целиком: триггер, шаги сценария, ожидаемый результат, последствия и журнал запусков.",
			objectSchema(map[string]any{
				"id": integerProp("ID автоматизации"),
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
				return nil, automationToolError(err)
			}
			return automationView(view), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_automation", "Спроектировать автоматизацию: триггер, шаги из инструментов LifeOS, ожидаемый результат и последствия. Автоматизация создаётся в статусе «предложено» и начнёт выполняться только после утверждения пользователем.",
			objectSchema(map[string]any{
				"title":           stringProp("Короткое название автоматизации"),
				"description":     stringProp("Какую повторяющуюся потребность она закрывает"),
				"expected_result": stringProp("Что должно получиться после каждого запуска"),
				"consequences":    stringProp("Что автоматизация реально делает и какие у этого риски"),
				"trigger":         triggerSchema(),
				"steps": map[string]any{
					"type": "array",
					"items": objectSchema(map[string]any{
						"name":        stringProp("Имя инструмента LifeOS"),
						"arguments":   map[string]any{"type": "object", "description": "Аргументы вызова инструмента"},
						"description": stringProp("Что делает этот шаг, по-человечески"),
					}, "name"),
					"description": "Шаги сценария по порядку; минимум один",
				},
			}, "title", "trigger", "steps")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			input, err := decodeAutomationInput(raw)
			if err != nil {
				return nil, err
			}
			view, err := service.Create(ctx, input)
			if err != nil {
				return nil, automationToolError(err)
			}
			result := automationView(view)
			result["note"] = "Автоматизация ждёт утверждения пользователя; выполняться до утверждения не будет"
			return result, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			input, err := decodeAutomationInput(raw)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Предложить автоматизацию «%s»: %s, шагов в сценарии: %d.",
				strings.TrimSpace(input.Title), describeTrigger(input.Trigger), len(input.Steps)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_automation", "Изменить автоматизацию: название, описание, триггер, шаги, ожидаемый результат или последствия. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"automation_id":    integerProp("ID автоматизации"),
				"expected_version": integerProp("Версия из get_automation"),
				"title":            stringProp("Новое название"),
				"description":      stringProp("Новое описание"),
				"expected_result":  stringProp("Новый ожидаемый результат"),
				"consequences":     stringProp("Новое описание последствий"),
				"trigger":          triggerSchema(),
				"steps": map[string]any{
					"type":        "array",
					"items":       objectSchema(map[string]any{"name": stringProp("Имя инструмента"), "arguments": map[string]any{"type": "object"}, "description": stringProp("Что делает шаг")}, "name"),
					"description": "Полный новый список шагов",
				},
			}, "automation_id", "expected_version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				AutomationID    int64                `json:"automation_id"`
				ExpectedVersion int                  `json:"expected_version"`
				Title           *string              `json:"title"`
				Description     *string              `json:"description"`
				ExpectedResult  *string              `json:"expected_result"`
				Consequences    *string              `json:"consequences"`
				Trigger         *json.RawMessage     `json:"trigger"`
				Steps           *[]automationStepArg `json:"steps"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := appautomation.UpdateInput{
				Title: args.Title, Description: args.Description,
				ExpectedResult: args.ExpectedResult, Consequences: args.Consequences,
			}
			if args.Trigger != nil {
				trigger, err := decodeTrigger(*args.Trigger)
				if err != nil {
					return nil, err
				}
				input.Trigger = &trigger
			}
			if args.Steps != nil {
				steps, err := buildSteps(*args.Steps)
				if err != nil {
					return nil, err
				}
				input.Steps = steps
			}
			view, err := service.Update(ctx, args.AutomationID, args.ExpectedVersion, input)
			if err != nil {
				return nil, automationToolError(err)
			}
			return automationView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Изменить автоматизацию #%d (конфигурация и сценарий).", args.AutomationID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("approve_automation", "Утвердить предложенную автоматизацию и включить её выполнение. Это решение пользователя: вызывай только после явного согласия в диалоге.",
			objectSchema(map[string]any{
				"automation_id": integerProp("ID автоматизации"),
			}, "automation_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.Approve(ctx, args.AutomationID)
			if err != nil {
				return nil, automationToolError(err)
			}
			return automationView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Утвердить автоматизацию #%d и включить её выполнение.", args.AutomationID), nil
		},
	})

	for _, item := range []struct {
		name   string
		label  string
		status domainautomation.Status
	}{
		{"pause_automation", "Приостановить автоматизацию", domainautomation.StatusPaused},
		{"resume_automation", "Возобновить автоматизацию", domainautomation.StatusActive},
	} {
		status := item.status
		registry.Register(Tool{
			Spec: assistantSpec(item.name, item.label+".",
				objectSchema(map[string]any{
					"automation_id": integerProp("ID автоматизации"),
				}, "automation_id")),
			Direct: true,
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					AutomationID int64 `json:"automation_id"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				view, err := service.SetStatus(ctx, args.AutomationID, status)
				if err != nil {
					return nil, automationToolError(err)
				}
				return automationView(view), nil
			},
		})
	}

	registry.Register(Tool{
		Spec: assistantSpec("archive_automation", "Отправить автоматизацию в архив: она больше не выполняется, но журнал запусков остаётся. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"automation_id": integerProp("ID автоматизации"),
			}, "automation_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.SetStatus(ctx, args.AutomationID, domainautomation.StatusArchived)
			if err != nil {
				return nil, automationToolError(err)
			}
			return automationView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Отправить автоматизацию #%d в архив.", args.AutomationID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("run_automation", "Выполнить утверждённую автоматизацию прямо сейчас, не дожидаясь триггера. Имеет реальные эффекты: вызывай после явного согласия пользователя.",
			objectSchema(map[string]any{
				"automation_id": integerProp("ID автоматизации"),
			}, "automation_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			view, err := service.RunNow(ctx, args.AutomationID)
			if err != nil {
				return nil, automationToolError(err)
			}
			return automationView(view), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				AutomationID int64 `json:"automation_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Выполнить автоматизацию #%d прямо сейчас.", args.AutomationID), nil
		},
	})
}

// automationStepArg is one step as the model describes it.
type automationStepArg struct {
	Name        string         `json:"name"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	Description string         `json:"description,omitempty"`
}

func buildSteps(args []automationStepArg) ([]domainautomation.Step, error) {
	steps := make([]domainautomation.Step, 0, len(args))
	for _, arg := range args {
		var encoded json.RawMessage
		if arg.Arguments != nil {
			data, err := json.Marshal(arg.Arguments)
			if err != nil {
				return nil, fmt.Errorf("шаг %q: аргументы не кодируются в JSON", arg.Name)
			}
			encoded = data
		}
		steps = append(steps, domainautomation.Step{
			Name: arg.Name, Arguments: encoded, Description: arg.Description,
		})
	}
	return steps, nil
}

// triggerSchema describes the trigger union for the model.
func triggerSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": enumProp("schedule", "event", "condition"),
			"schedule": objectSchema(map[string]any{
				"frequency":    enumProp("daily", "weekly", "monthly"),
				"time":         stringProp("Время по местному времени, HH:MM"),
				"weekdays":     map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Для weekly: дни недели, 0 = воскресенье … 6 = суббота"},
				"day_of_month": integerProp("Для monthly: день месяца 1–31; 0 — последний день месяца"),
			}),
			"event": objectSchema(map[string]any{
				"name":  stringProp("Тип события LifeOS, например task.completed"),
				"match": map[string]any{"type": "object", "description": "Необязательные поля payload, которые должны совпасть"},
			}),
			"condition": objectSchema(map[string]any{
				"insight":          stringProp("Ключ наблюдения, например overdue или inbox-backlog"),
				"cooldown_minutes": integerProp("Минимальный интервал между запусками в минутах (по умолчанию 360)"),
			}),
		},
		"required": []string{"kind"},
	}
}

func decodeTrigger(raw json.RawMessage) (domainautomation.Trigger, error) {
	var trigger domainautomation.Trigger
	if err := json.Unmarshal(raw, &trigger); err != nil {
		return domainautomation.Trigger{}, fmt.Errorf("триггер: %w", err)
	}
	return trigger, nil
}

func decodeAutomationInput(raw json.RawMessage) (appautomation.CreateInput, error) {
	var args struct {
		Title          string              `json:"title"`
		Description    string              `json:"description"`
		ExpectedResult string              `json:"expected_result"`
		Consequences   string              `json:"consequences"`
		Trigger        json.RawMessage     `json:"trigger"`
		Steps          []automationStepArg `json:"steps"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return appautomation.CreateInput{}, err
	}
	trigger, err := decodeTrigger(args.Trigger)
	if err != nil {
		return appautomation.CreateInput{}, err
	}
	steps, err := buildSteps(args.Steps)
	if err != nil {
		return appautomation.CreateInput{}, err
	}
	return appautomation.CreateInput{
		Title: args.Title, Description: args.Description,
		Trigger: trigger, Steps: steps,
		ExpectedResult: args.ExpectedResult, Consequences: args.Consequences,
	}, nil
}

// automationToolError keeps domain and service errors readable in chat.
func automationToolError(err error) error {
	switch {
	case errors.Is(err, appautomation.ErrNotFound):
		return fmt.Errorf("автоматизация не найдена")
	case errors.Is(err, appautomation.ErrRunNotFound):
		return fmt.Errorf("запуск не найден")
	case errors.Is(err, appautomation.ErrVersionConflict):
		return fmt.Errorf("автоматизация изменена кем-то ещё; перечитай её и повтори с актуальной версией")
	case errors.Is(err, appautomation.ErrInvalidTransition):
		return fmt.Errorf("%w", err)
	case errors.Is(err, appautomation.ErrStepNotAllowed):
		return fmt.Errorf("%w", err)
	case errors.Is(err, appautomation.ErrNotExecutable):
		return fmt.Errorf("%w", err)
	}
	return err
}

func automationSummaryView(view *appautomation.View) map[string]any {
	a := view.Automation
	body := map[string]any{
		"id":          a.ID,
		"title":       a.Title,
		"status":      string(a.Status),
		"trigger":     describeTrigger(a.Trigger),
		"trigger_raw": a.Trigger,
		"steps":       len(a.Steps),
		"created_at":  a.CreatedAt.Format(time.RFC3339),
		"updated_at":  a.UpdatedAt.Format(time.RFC3339),
		"version":     a.Version,
		"next_run_at": formatAutomationTime(a.NextRunAt),
		"last_run_at": formatAutomationTime(a.LastRunAt),
	}
	if len(view.Runs) > 0 {
		latest := view.Runs[0]
		body["latest_run"] = map[string]any{
			"id":         latest.ID,
			"status":     string(latest.Status),
			"trigger":    latest.Trigger,
			"started_at": latest.StartedAt.Format(time.RFC3339),
			"summary":    latest.Summary,
		}
	}
	return body
}

func automationView(view *appautomation.View) map[string]any {
	a := view.Automation
	body := automationSummaryView(view)
	body["description"] = a.Description
	body["expected_result"] = a.ExpectedResult
	body["consequences"] = a.Consequences
	body["approved_at"] = formatAutomationTime(a.ApprovedAt)
	steps := make([]map[string]any, 0, len(a.Steps))
	for i, step := range a.Steps {
		item := map[string]any{
			"position":    i + 1,
			"name":        step.Name,
			"description": step.Description,
		}
		if len(step.Arguments) > 0 {
			var decoded any
			if json.Unmarshal(step.Arguments, &decoded) == nil {
				item["arguments"] = decoded
			}
		}
		steps = append(steps, item)
	}
	body["scenario"] = steps
	runs := make([]map[string]any, 0, len(view.Runs))
	for _, run := range view.Runs {
		runs = append(runs, runView(run))
	}
	body["runs"] = runs
	return body
}

func runView(run *domainautomation.Run) map[string]any {
	body := map[string]any{
		"id":          run.ID,
		"trigger":     run.Trigger,
		"status":      string(run.Status),
		"summary":     run.Summary,
		"started_at":  run.StartedAt.Format(time.RFC3339),
		"finished_at": formatAutomationTime(run.FinishedAt),
	}
	if run.Error != "" {
		body["error"] = run.Error
	}
	steps := make([]map[string]any, 0, len(run.Steps))
	for _, step := range run.Steps {
		item := map[string]any{
			"name":        step.Name,
			"status":      string(step.Status),
			"duration_ms": step.DurationMS,
		}
		if step.Output != "" {
			item["output"] = step.Output
		}
		if step.Error != "" {
			item["error"] = step.Error
		}
		steps = append(steps, item)
	}
	body["steps"] = steps
	return body
}

// describeTrigger renders the trigger in user language.
func describeTrigger(trigger domainautomation.Trigger) string {
	switch trigger.Kind {
	case domainautomation.TriggerSchedule:
		schedule := trigger.Schedule
		if schedule == nil {
			return "по времени"
		}
		cadence := ""
		switch schedule.Frequency {
		case domainautomation.FrequencyDaily:
			cadence = "ежедневно"
		case domainautomation.FrequencyWeekly:
			days := make([]string, 0, len(schedule.Weekdays))
			for _, day := range schedule.Weekdays {
				days = append(days, weekdayName(day))
			}
			cadence = "еженедельно по " + strings.Join(days, ", ")
		case domainautomation.FrequencyMonthly:
			if schedule.DayOfMonth == 0 {
				cadence = "ежемесячно в последний день месяца"
			} else {
				cadence = fmt.Sprintf("ежемесячно %d числа", schedule.DayOfMonth)
			}
		}
		return fmt.Sprintf("%s в %s", cadence, schedule.Time)
	case domainautomation.TriggerEvent:
		if trigger.Event == nil {
			return "по событию"
		}
		return fmt.Sprintf("по событию %s", trigger.Event.Name)
	case domainautomation.TriggerCondition:
		if trigger.Condition == nil {
			return "по условию"
		}
		return fmt.Sprintf("пока наблюдение «%s» активно", trigger.Condition.Insight)
	}
	return string(trigger.Kind)
}

func weekdayName(day int) string {
	names := []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}
	if day >= 0 && day < len(names) {
		return names[day]
	}
	return "?"
}

func formatAutomationTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339)
}
