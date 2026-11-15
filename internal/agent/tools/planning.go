package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/application/recurrence"
	domaininbox "github.com/theDenuvo/LifeOS/internal/domain/inbox"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// TodayProvider resolves "today" in the configured timezone.
type TodayProvider func() (task.CalendarDate, error)

// registerPlanningTools adds the time-management tools:
//
//	plan_day      read     what the day holds and what the planner proposes
//	apply_plan    mutation accept a plan (writes work blocks)
//	replan        read     propose moves for overdue and deadline work
//	estimate_duration read  how long similar work took before
//	review_habit  read     whether a recurring task keeps slipping
//	capture       mutation keep an idea or note for later, with a revisit date
//	list_ideas    read     captures whose day has come
func registerPlanningTools(
	registry *Registry,
	planner *planning.Service,
	habits *recurrence.Service,
	capture func(ctx context.Context, in captureInput) (captureResult, error),
	today TodayProvider,
) {
	registry.Register(Tool{
		Spec: assistantSpec("plan_day", "План дня: что уже стоит в календаре, какие блоки работы есть, что планировщик предлагает и что не помещается.", objectSchema(map[string]any{
			"date": stringProp("Дата YYYY-MM-DD; по умолчанию сегодня"),
		})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Date string `json:"date"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			date, err := resolveDate(args.Date, today)
			if err != nil {
				return nil, err
			}
			plan, err := planner.PlanDay(ctx, date)
			if err != nil {
				return nil, err
			}
			return planView(plan), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("apply_plan", "Записать план: забронировать время в календаре под задачи. Требует подтверждения пользователя.", objectSchema(map[string]any{
			"date": stringProp("Дата YYYY-MM-DD"),
			"items": map[string]any{
				"type":        "array",
				"description": "Блоки работы: задача и время",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task_id": integerProp("ID задачи"),
						"start":   stringProp("Начало RFC3339"),
						"end":     stringProp("Конец RFC3339"),
					},
					"required": []string{"task_id", "start", "end"},
				},
			},
		}, "date", "items")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Date  string `json:"date"`
				Items []struct {
					TaskID int64  `json:"task_id"`
					Start  string `json:"start"`
					End    string `json:"end"`
				} `json:"items"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			date, err := resolveDate(args.Date, today)
			if err != nil {
				return nil, err
			}
			in := planning.ApplyInput{ReplaceDates: []string{date.String()}}
			for _, item := range args.Items {
				start, err := time.Parse(time.RFC3339, item.Start)
				if err != nil {
					return nil, fmt.Errorf("start must be RFC3339: %w", err)
				}
				end, err := time.Parse(time.RFC3339, item.End)
				if err != nil {
					return nil, fmt.Errorf("end must be RFC3339: %w", err)
				}
				in.Items = append(in.Items, planning.PlacementInput{TaskID: item.TaskID, Start: start, End: end})
			}
			result, err := planner.Apply(ctx, in)
			if err != nil {
				return nil, err
			}
			applied := make([]map[string]any, 0, len(result.Applied))
			for _, item := range result.Applied {
				applied = append(applied, map[string]any{
					"task_id": item.TaskID,
					"title":   item.Title,
					"start":   item.Start.Format(time.RFC3339),
					"end":     item.End.Format(time.RFC3339),
				})
			}
			return map[string]any{"applied": applied, "count": len(applied)}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Items []struct {
					TaskID int64  `json:"task_id"`
					Start  string `json:"start"`
				} `json:"items"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Забронировать %d блок(ов) времени под задачи", len(args.Items)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("replan", "Предложить перенос просроченных и рискованных задач на свободное время. Ничего не меняет: только варианты.", objectSchema(map[string]any{
			"days": integerProp("Горизонт планирования в днях (1-30, по умолчанию 7)"),
		})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Days int `json:"days"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			proposal, err := planner.Replan(ctx, args.Days)
			if err != nil {
				return nil, err
			}
			moves := make([]map[string]any, 0, len(proposal.Moves))
			for _, item := range proposal.Moves {
				entry := map[string]any{
					"task_id":          item.TaskID,
					"title":            item.Title,
					"to":               item.To,
					"reason":           item.Reason,
					"estimate_minutes": item.Estimate,
				}
				if item.From != "" {
					entry["from"] = item.From
				}
				moves = append(moves, entry)
			}
			placements := make([]map[string]any, 0, len(proposal.Placements))
			for _, item := range proposal.Placements {
				placements = append(placements, map[string]any{
					"task_id": item.TaskID,
					"date":    item.Date,
					"start":   item.Start.Format(time.RFC3339),
					"end":     item.End.Format(time.RFC3339),
				})
			}
			unplaced := make([]map[string]any, 0, len(proposal.Unplaced))
			for _, item := range proposal.Unplaced {
				unplaced = append(unplaced, map[string]any{
					"task_id":          item.TaskID,
					"title":            item.Title,
					"estimate_minutes": item.EstimateMin,
					"estimate_source":  string(item.Source),
					"reason":           item.Reason,
				})
			}
			kept := make([]map[string]any, 0, len(proposal.Kept))
			for _, item := range proposal.Kept {
				kept = append(kept, map[string]any{
					"task_id": item.TaskID,
					"title":   item.Title,
					"reason":  item.Reason,
					"start":   item.Start.Format(time.RFC3339),
					"end":     item.End.Format(time.RFC3339),
					"source":  item.Source,
				})
			}
			return map[string]any{
				"summary":        proposal.Summary,
				"moves":          moves,
				"placements":     placements,
				"unplaced":       unplaced,
				"already_placed": kept,
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("estimate_duration", "Сколько времени занимала похожая работа раньше. Используй, когда у задачи нет оценки.", objectSchema(map[string]any{
			"title": stringProp("Название задачи"),
			"tags":  stringProp("Теги через запятую"),
		}, "title")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title string `json:"title"`
				Tags  string `json:"tags"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			suggestion, err := planner.SuggestEstimate(ctx, args.Title, splitTags(args.Tags))
			if err != nil {
				return nil, err
			}
			out := map[string]any{
				"minutes":  suggestion.Minutes,
				"source":   suggestion.Source,
				"samples":  suggestion.Samples,
				"based_on": suggestion.BasedOn,
			}
			if suggestion.Question != "" {
				out["question"] = suggestion.Question
			}
			return out, nil
		},
	})

	if habits != nil {
		registry.Register(Tool{
			Spec: assistantSpec("review_habit", "Проверить, соблюдается ли ритм повторяющейся задачи, и предложить изменение интервала.", objectSchema(map[string]any{
				"task_id": integerProp("ID повторяющейся задачи"),
			}, "task_id")),
			ReadOnly: true,
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					TaskID int64 `json:"task_id"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				review, err := habits.Review(ctx, args.TaskID)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"task_id":            review.TaskID,
					"title":              review.Title,
					"repeat":             review.Repeat,
					"samples":            review.Samples,
					"average_delay_days": review.AverageDelay,
					"advice":             review.Advice,
				}, nil
			},
		})
	}

	if capture != nil {
		registry.Register(Tool{
			Spec: assistantSpec("capture", "Сохранить мысль, идею или заметку, чтобы вернуться к ней позже. Не создаёт задачу. Требует подтверждения пользователя.", objectSchema(map[string]any{
				"text":       stringProp("Текст как его сформулировал пользователь"),
				"kind":       enumProp("idea", "note", "goal", "task"),
				"revisit_at": stringProp("Дата, когда напомнить, YYYY-MM-DD"),
			}, "text", "kind")),
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Text      string `json:"text"`
					Kind      string `json:"kind"`
					RevisitAt string `json:"revisit_at"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				kind := domaininbox.Kind(args.Kind)
				if !kind.IsValid() {
					return nil, fmt.Errorf("kind must be idea, note, goal or task")
				}
				var revisit *task.CalendarDate
				if args.RevisitAt != "" {
					parsed, err := task.ParseCalendarDate(args.RevisitAt)
					if err != nil {
						return nil, fmt.Errorf("revisit_at must be YYYY-MM-DD: %w", err)
					}
					revisit = &parsed
				}
				saved, err := capture(ctx, captureInput{Text: args.Text, Kind: kind, RevisitAt: revisit})
				if err != nil {
					return nil, err
				}
				out := map[string]any{"id": saved.ID, "kind": string(saved.Kind), "text": saved.Text}
				if saved.RevisitAt != nil && !saved.RevisitAt.IsZero() {
					out["revisit_at"] = saved.RevisitAt.String()
				}
				return out, nil
			},
			Preview: func(raw json.RawMessage) (string, error) {
				var args struct {
					Text string `json:"text"`
					Kind string `json:"kind"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				label := map[string]string{"idea": "идею", "note": "заметку", "goal": "цель", "task": "запись"}[args.Kind]
				return fmt.Sprintf("Сохранить %s «%s»", label, args.Text), nil
			},
		})
	}
}

// captureInput is what the capture tool asks the application layer to store.
type captureInput struct {
	Text      string
	Kind      domaininbox.Kind
	RevisitAt *task.CalendarDate
}

// captureResult is the stored capture.
type captureResult struct {
	ID        int64
	Text      string
	Kind      domaininbox.Kind
	RevisitAt *task.CalendarDate
}

// planView renders a day plan for the model.
func planView(plan planning.DayPlan) map[string]any {
	blocks := make([]map[string]any, 0, len(plan.Blocks))
	for _, item := range plan.Blocks {
		blocks = append(blocks, map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"start":            item.Start.Format(time.RFC3339),
			"end":              item.End.Format(time.RFC3339),
			"source":           string(item.Source),
			"estimate_minutes": item.Estimate,
			"recurring":        item.Recurring,
		})
	}
	busy := make([]map[string]any, 0, len(plan.Busy))
	for _, item := range plan.Busy {
		busy = append(busy, map[string]any{
			"label": item.Label,
			"kind":  item.Kind,
			"start": item.Start.Format(time.RFC3339),
			"end":   item.End.Format(time.RFC3339),
		})
	}
	proposed := make([]map[string]any, 0, len(plan.Proposed))
	for _, item := range plan.Proposed {
		proposed = append(proposed, map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"start":            item.Start.Format(time.RFC3339),
			"end":              item.End.Format(time.RFC3339),
			"estimate_minutes": item.Estimate,
			"estimate_source":  string(item.EstimateSource),
			"recurring":        item.Recurring,
		})
	}
	unplaced := make([]map[string]any, 0, len(plan.Unplaced))
	for _, item := range plan.Unplaced {
		unplaced = append(unplaced, map[string]any{
			"task_id":          item.TaskID,
			"title":            item.Title,
			"estimate_minutes": item.EstimateMin,
			"estimate_source":  string(item.Source),
			"reason":           item.Reason,
		})
	}
	return map[string]any{
		"date":            plan.Date,
		"working":         plan.Working,
		"past":            plan.Past,
		"workday":         plan.WorkdayStart + "-" + plan.WorkdayEnd,
		"free_minutes":    plan.FreeMinutes,
		"planned_minutes": plan.PlannedMinutes,
		"blocks":          blocks,
		"busy":            busy,
		"proposed":        proposed,
		"unplaced":        unplaced,
	}
}

// resolveDate parses an optional ISO date, defaulting to today.
func resolveDate(raw string, today TodayProvider) (task.CalendarDate, error) {
	if raw != "" {
		return task.ParseCalendarDate(raw)
	}
	if today == nil {
		return task.CalendarDate{}, fmt.Errorf("date is required")
	}
	return today()
}

// splitTags reads a comma-separated tag list from tool arguments.
func splitTags(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			part := raw[start:i]
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			for len(part) > 0 && part[len(part)-1] == ' ' {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
