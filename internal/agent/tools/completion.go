package tools

import (
	"context"
	"encoding/json"

	"github.com/theDenuvo/LifeOS/internal/application/completion"
)

// registerCompletionTools adds the completion check:
//
//	verify_completion  read  compare the expected result with the available sources
//
// The tool reports the verdict together with the sources it consulted and the
// evidence it found. It never marks the task done by itself: closing work on the
// user's behalf requires either their decision or the explicit auto-complete
// setting, so the assistant cannot plausibly claim a task is finished.
func registerCompletionTools(registry *Registry, checker *completion.Service) {
	registry.Register(Tool{
		Spec: assistantSpec("verify_completion", "Проверить, выполнен ли ожидаемый результат задачи, по доступным источникам (документы, файлы, календарь). Возвращает вердикт, уверенность, источник и основание. Статус задачи не меняет.", objectSchema(map[string]any{
			"task_id": integerProp("ID задачи"),
		}, "task_id")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				TaskID int64 `json:"task_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			report, err := checker.Check(ctx, args.TaskID)
			if err != nil {
				return nil, err
			}
			return completionView(report), nil
		},
	})
}

// completionView is the assistant-facing shape of a check.
func completionView(report completion.Report) map[string]any {
	sources := make([]map[string]any, 0, len(report.Sources))
	for _, item := range report.Sources {
		sources = append(sources, map[string]any{
			"name":      item.Name,
			"available": item.Available,
			"reason":    item.Reason,
		})
	}
	evidence := make([]map[string]any, 0, len(report.Evidence))
	for _, item := range report.Evidence {
		evidence = append(evidence, map[string]any{
			"source":     item.Source,
			"reference":  item.Reference,
			"snippet":    item.Snippet,
			"confidence": item.Confidence,
			"against":    item.Against,
		})
	}
	view := map[string]any{
		"task_id":    report.TaskID,
		"title":      report.Title,
		"verdict":    string(report.Verdict),
		"confidence": report.Confidence,
		"reason":     report.Reason,
		"sources":    sources,
		"evidence":   evidence,
	}
	if report.Expected != "" {
		view["expected_result"] = report.Expected
	}
	return view
}
