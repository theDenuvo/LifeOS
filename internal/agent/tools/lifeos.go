package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// NewLifeOSTools builds the registry over the application services.
func NewLifeOSTools(taskService *tasks.Service, inboxService *inbox.Service, summaryService *summary.Service) *Registry {
	registry := NewRegistry()

	registry.Register(Tool{
		Spec: assistantSpec("list_tasks", "Список задач с фильтрами. Используй для чтения состояния.",
			objectSchema(map[string]any{
				"status":   enumProp("open", "completed", "cancelled"),
				"priority": enumProp("low", "medium", "high"),
				"search":   stringProp("Поиск по названию и заметке"),
				"limit":    integerProp("Сколько задач вернуть (1-50, по умолчанию 20)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Status   string `json:"status"`
				Priority string `json:"priority"`
				Search   string `json:"search"`
				Limit    int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			filter := tasks.TaskFilter{Search: tasks.Some(args.Search)}
			if args.Status != "" {
				status := task.Status(args.Status)
				if !status.IsValid() {
					return nil, fmt.Errorf("invalid status %q", args.Status)
				}
				filter.Statuses = []task.Status{status}
			}
			if args.Priority != "" {
				priority := task.Priority(args.Priority)
				if !priority.IsValid() {
					return nil, fmt.Errorf("invalid priority %q", args.Priority)
				}
				filter.Priorities = []task.Priority{priority}
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 20
			}
			if limit > 50 {
				limit = 50
			}
			filter.Limit = tasks.Some(limit)

			result, err := taskService.ListTasks(ctx, filter)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(result.Tasks))
			for _, item := range result.Tasks {
				items = append(items, taskView(item))
			}
			return map[string]any{"tasks": items, "total": result.Total}, nil
		},
	})

	registry.Register(Tool{
		Spec:     assistantSpec("get_today_summary", "Сводка дня: задачи на сегодня, просроченные, приоритеты и статистика.", objectSchema(nil)),
		ReadOnly: true,
		Execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
			return summaryService.Today(ctx)
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("list_inbox", "Необработанные входящие элементы.", objectSchema(map[string]any{
			"limit": integerProp("Сколько элементов вернуть (1-50)"),
		})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Limit int `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 20
			}
			if limit > 50 {
				limit = 50
			}
			result, err := inboxService.List(ctx, inbox.Filter{UnprocessedOnly: true, Limit: tasks.Some(limit)})
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(result.Items))
			for _, item := range result.Items {
				items = append(items, map[string]any{
					"id":         item.ID,
					"text":       item.Text,
					"created_at": item.CreatedAt.Format(time.RFC3339),
				})
			}
			return map[string]any{"items": items, "total": result.Total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_task", "Создать задачу. Требует подтверждения пользователя.", objectSchema(map[string]any{
			"title":    stringProp("Название задачи"),
			"note":     stringProp("Заметка"),
			"priority": enumProp("low", "medium", "high"),
			"due_date": stringProp("Календарный дедлайн YYYY-MM-DD"),
			"due_at":   stringProp("Точный дедлайн RFC3339"),
		}, "title")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title    string  `json:"title"`
				Note     *string `json:"note"`
				Priority *string `json:"priority"`
				DueDate  *string `json:"due_date"`
				DueAt    *string `json:"due_at"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := tasks.CreateTaskInput{Title: args.Title}
			if args.Note != nil {
				input.Note = tasks.Some(*args.Note)
			}
			if args.Priority != nil {
				input.Priority = tasks.Some(task.Priority(*args.Priority))
			}
			planned, err := calendarOptional(args.DueDate)
			if err != nil {
				return nil, err
			}
			input.DueDate = planned
			dueAt, err := timeOptional(args.DueAt)
			if err != nil {
				return nil, err
			}
			input.DueAt = dueAt

			created, err := taskService.CreateTask(ctx, input, task.ActorAssistant)
			if err != nil {
				return nil, err
			}
			return taskView(created), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Title string `json:"title"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Создать задачу «%s»", args.Title), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_task", "Изменить задачу. Требует подтверждения пользователя.", objectSchema(map[string]any{
			"id":       integerProp("ID задачи"),
			"version":  integerProp("Версия задачи из последнего чтения"),
			"title":    stringProp("Новое название"),
			"note":     stringProp("Новая заметка; пустая строка очищает"),
			"priority": enumProp("low", "medium", "high"),
			"due_date": stringProp("Новый календарный дедлайн YYYY-MM-DD; пустая строка очищает"),
			"due_at":   stringProp("Новый точный дедлайн RFC3339; пустая строка очищает"),
		}, "id", "version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID       int64   `json:"id"`
				Version  int     `json:"version"`
				Title    *string `json:"title"`
				Note     *string `json:"note"`
				Priority *string `json:"priority"`
				DueDate  *string `json:"due_date"`
				DueAt    *string `json:"due_at"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := tasks.UpdateTaskInput{}
			if args.Title != nil {
				input.Title = tasks.Some(*args.Title)
			}
			if args.Note != nil {
				input.Note = tasks.Some(*args.Note)
			}
			if args.Priority != nil {
				input.Priority = tasks.Some(task.Priority(*args.Priority))
			}
			if args.DueDate != nil {
				value, err := calendarOptional(args.DueDate)
				if err != nil {
					return nil, err
				}
				input.DueDate = value
			}
			if args.DueAt != nil {
				value, err := timeOptional(args.DueAt)
				if err != nil {
					return nil, err
				}
				input.DueAt = value
			}

			updated, err := taskService.UpdateTask(ctx, args.ID, args.Version, input, task.ActorAssistant)
			if err != nil {
				return nil, err
			}
			return taskView(updated), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Изменить задачу #%d", args.ID), nil
		},
	})

	registerTransition(registry, "complete_task", "Завершить задачу", taskService.CompleteTask)
	registerTransition(registry, "cancel_task", "Отменить задачу", taskService.CancelTask)
	registerTransition(registry, "restore_task", "Восстановить задачу", taskService.RestoreTask)

	registry.Register(Tool{
		Spec: assistantSpec("reschedule_task", "Перенести задачу. Требует подтверждения пользователя.", objectSchema(map[string]any{
			"id":       integerProp("ID задачи"),
			"version":  integerProp("Версия задачи"),
			"due_date": stringProp("Новый календарный дедлайн YYYY-MM-DD; пустая строка очищает"),
			"due_at":   stringProp("Новый точный дедлайн RFC3339; пустая строка очищает"),
		}, "id", "version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID      int64   `json:"id"`
				Version int     `json:"version"`
				DueDate *string `json:"due_date"`
				DueAt   *string `json:"due_at"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := tasks.RescheduleTaskInput{}
			if args.DueDate != nil {
				value, err := calendarOptional(args.DueDate)
				if err != nil {
					return nil, err
				}
				input.DueDate = value
			}
			if args.DueAt != nil {
				value, err := timeOptional(args.DueAt)
				if err != nil {
					return nil, err
				}
				input.DueAt = value
			}
			updated, err := taskService.RescheduleTask(ctx, args.ID, args.Version, input, task.ActorAssistant)
			if err != nil {
				return nil, err
			}
			return taskView(updated), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Перенести задачу #%d", args.ID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("capture_inbox", "Добавить мысль или задачу во входящие. Требует подтверждения.", objectSchema(map[string]any{
			"text": stringProp("Текст для входящих"),
		}, "text")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Text string `json:"text"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			item, err := inboxService.Capture(ctx, args.Text, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": item.ID, "text": item.Text}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Text string `json:"text"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Добавить во входящие: «%s»", args.Text), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("convert_inbox", "Преобразовать входящий элемент в задачу. Требует подтверждения.", objectSchema(map[string]any{
			"id":       integerProp("ID входящего элемента"),
			"priority": enumProp("low", "medium", "high"),
		}, "id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID       int64   `json:"id"`
				Priority *string `json:"priority"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := inbox.ConvertInput{}
			if args.Priority != nil {
				input.Priority = tasks.Some(task.Priority(*args.Priority))
			}
			created, err := inboxService.ConvertToTask(ctx, args.ID, input)
			if err != nil {
				return nil, err
			}
			return taskView(created), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Преобразовать входящий #%d в задачу", args.ID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("dismiss_inbox", "Удалить входящий элемент. Требует подтверждения.", objectSchema(map[string]any{
			"id": integerProp("ID входящего элемента"),
		}, "id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := inboxService.Dismiss(ctx, args.ID); err != nil {
				return nil, err
			}
			return map[string]any{"dismissed": args.ID}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Удалить входящий #%d", args.ID), nil
		},
	})

	return registry
}

type transitionFunc func(ctx context.Context, id int64, version int, actor task.Actor) (tasks.TaskWithVersion, error)

func registerTransition(registry *Registry, name, label string, apply transitionFunc) {
	registry.Register(Tool{
		Spec: assistantSpec(name, label+". Требует подтверждения пользователя.", objectSchema(map[string]any{
			"id":      integerProp("ID задачи"),
			"version": integerProp("Версия задачи"),
		}, "id", "version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID      int64 `json:"id"`
				Version int   `json:"version"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			updated, err := apply(ctx, args.ID, args.Version, task.ActorAssistant)
			if err != nil {
				return nil, err
			}
			return taskView(updated), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s #%d", label, args.ID), nil
		},
	})
}

func taskView(item tasks.TaskWithVersion) map[string]any {
	t := item.Task
	view := map[string]any{
		"id":       t.ID,
		"title":    t.Title,
		"priority": string(t.Priority),
		"status":   string(t.Status),
		"timezone": t.Timezone,
		"tags":     t.Tags,
		"version":  item.Version,
	}
	if t.Note != nil {
		view["note"] = *t.Note
	}
	if t.PlannedDate != nil && !t.PlannedDate.IsZero() {
		view["planned_date"] = t.PlannedDate.String()
	}
	if t.DueDate != nil && !t.DueDate.IsZero() {
		view["due_date"] = t.DueDate.String()
	}
	if t.DueAt != nil {
		view["due_at"] = t.DueAt.Format(time.RFC3339)
	}
	return view
}

func decodeArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func calendarOptional(value *string) (tasks.Optional[task.CalendarDate], error) {
	if value == nil {
		return tasks.None[task.CalendarDate](), nil
	}
	if *value == "" {
		return tasks.Some(task.CalendarDate{}), nil
	}
	date, err := task.ParseCalendarDate(*value)
	if err != nil {
		return tasks.None[task.CalendarDate](), err
	}
	return tasks.Some(date), nil
}

func timeOptional(value *string) (tasks.Optional[time.Time], error) {
	if value == nil {
		return tasks.None[time.Time](), nil
	}
	if *value == "" {
		return tasks.Some(time.Time{}), nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return tasks.None[time.Time](), fmt.Errorf("due_at must be RFC3339")
	}
	return tasks.Some(parsed.UTC()), nil
}

func assistantSpec(name, description string, parameters json.RawMessage) assistant.ToolSpec {
	return assistant.ToolSpec{Name: name, Description: description, Parameters: parameters}
}

func objectSchema(properties map[string]any, required ...string) json.RawMessage {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	encoded, _ := json.Marshal(schema)
	return encoded
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func enumProp(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
