package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// registerEventTools adds calendar tools to the assistant registry.
func registerEventTools(registry *Registry, service *appevents.Service, timezone string) {
	registry.Register(Tool{
		Spec: assistantSpec("list_events", "События календаря за диапазон дат. Используй для чтения расписания.",
			objectSchema(map[string]any{
				"from":     stringProp("Начальная дата YYYY-MM-DD; по умолчанию сегодня"),
				"to":       stringProp("Конечная дата YYYY-MM-DD; по умолчанию через 7 дней"),
				"calendar": enumProp("work", "meetings", "personal"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				From     string `json:"from"`
				To       string `json:"to"`
				Calendar string `json:"calendar"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				loc = time.UTC
			}
			now := time.Now().In(loc)
			from, err := eventDate(args.From, now, 0)
			if err != nil {
				return nil, err
			}
			to, err := eventDate(args.To, now, 6)
			if err != nil {
				return nil, err
			}
			var calendars []event.Calendar
			if args.Calendar != "" {
				value := event.Calendar(args.Calendar)
				if !value.IsValid() {
					return nil, fmt.Errorf("invalid calendar %q", args.Calendar)
				}
				calendars = []event.Calendar{value}
			}
			result, err := service.ListRange(ctx, from.Time(loc), to.Time(loc).AddDate(0, 0, 1), calendars)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(result))
			for _, item := range result {
				items = append(items, eventView(item))
			}
			return map[string]any{"events": items, "total": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("get_event", "Получить событие по ID.", objectSchema(map[string]any{
			"id": integerProp("ID события"),
		}, "id")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			found, err := service.Get(ctx, args.ID)
			if err != nil {
				return nil, err
			}
			return eventView(found), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_event", "Создать событие в календаре. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"title":       stringProp("Название события"),
				"starts_at":   stringProp("Начало, RFC3339"),
				"ends_at":     stringProp("Окончание, RFC3339; по умолчанию +1 час"),
				"calendar":    enumProp("work", "meetings", "personal"),
				"location":    stringProp("Место"),
				"description": stringProp("Описание"),
				"all_day":     map[string]any{"type": "boolean", "description": "Событие на весь день"},
			}, "title", "starts_at")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title       string  `json:"title"`
				StartsAt    string  `json:"starts_at"`
				EndsAt      *string `json:"ends_at"`
				Calendar    string  `json:"calendar"`
				Location    *string `json:"location"`
				Description *string `json:"description"`
				AllDay      bool    `json:"all_day"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			startsAt, err := time.Parse(time.RFC3339, args.StartsAt)
			if err != nil {
				return nil, fmt.Errorf("starts_at must be RFC3339")
			}
			var endsAt *time.Time
			if args.EndsAt != nil && *args.EndsAt != "" {
				parsed, err := time.Parse(time.RFC3339, *args.EndsAt)
				if err != nil {
					return nil, fmt.Errorf("ends_at must be RFC3339")
				}
				endsAt = &parsed
			}
			created, err := service.Create(ctx, appevents.CreateEventInput{
				Title:       args.Title,
				Description: args.Description,
				Location:    args.Location,
				Calendar:    args.Calendar,
				StartsAt:    startsAt,
				EndsAt:      endsAt,
				AllDay:      args.AllDay,
			})
			if err != nil {
				return nil, err
			}
			return eventView(created), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Title    string `json:"title"`
				StartsAt string `json:"starts_at"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Создать событие «%s»", args.Title), nil
		},
	})

	// Bulk creation exists because a schedule import is one user intent, not
	// forty round trips: a single call keeps a 40-row Excel import inside one
	// tool round and one confirmation.
	registry.Register(Tool{
		Spec: assistantSpec("create_events", "Создать несколько событий календаря за один вызов. Используй для массового импорта расписания вместо повторных create_event.",
			objectSchema(map[string]any{
				"events": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    200,
					"description": "События для создания",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":       stringProp("Название события"),
							"starts_at":   stringProp("Начало, RFC3339"),
							"ends_at":     stringProp("Окончание, RFC3339; по умолчанию +1 час"),
							"calendar":    enumProp("work", "meetings", "personal"),
							"location":    stringProp("Место"),
							"description": stringProp("Описание"),
							"all_day":     booleanProp("Событие на весь день"),
						},
						"required":             []string{"title", "starts_at"},
						"additionalProperties": false,
					},
				},
			}, "events")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Events []struct {
					Title       string  `json:"title"`
					StartsAt    string  `json:"starts_at"`
					EndsAt      *string `json:"ends_at"`
					Calendar    string  `json:"calendar"`
					Location    *string `json:"location"`
					Description *string `json:"description"`
					AllDay      bool    `json:"all_day"`
				} `json:"events"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if len(args.Events) == 0 {
				return nil, fmt.Errorf("events не может быть пустым")
			}
			if len(args.Events) > 200 {
				return nil, fmt.Errorf("за один раз можно создать не более 200 событий")
			}
			created := make([]map[string]any, 0, len(args.Events))
			failed := make([]map[string]any, 0)
			for index, item := range args.Events {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				startsAt, err := time.Parse(time.RFC3339, item.StartsAt)
				if err != nil {
					failed = append(failed, map[string]any{"index": index, "title": item.Title, "error": "starts_at must be RFC3339"})
					continue
				}
				var endsAt *time.Time
				if item.EndsAt != nil && *item.EndsAt != "" {
					parsed, err := time.Parse(time.RFC3339, *item.EndsAt)
					if err != nil {
						failed = append(failed, map[string]any{"index": index, "title": item.Title, "error": "ends_at must be RFC3339"})
						continue
					}
					endsAt = &parsed
				}
				view, err := service.Create(ctx, appevents.CreateEventInput{
					Title:       item.Title,
					Description: item.Description,
					Location:    item.Location,
					Calendar:    item.Calendar,
					StartsAt:    startsAt,
					EndsAt:      endsAt,
					AllDay:      item.AllDay,
				})
				if err != nil {
					failed = append(failed, map[string]any{"index": index, "title": item.Title, "error": err.Error()})
					continue
				}
				created = append(created, eventView(view))
			}
			return map[string]any{
				"created":       created,
				"created_count": len(created),
				"failed":        failed,
				"failed_count":  len(failed),
			}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Events []struct {
					Title    string `json:"title"`
					StartsAt string `json:"starts_at"`
				} `json:"events"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if len(args.Events) == 0 {
				return "Создать события в календаре.", nil
			}
			shown := make([]string, 0, 4)
			for index, item := range args.Events {
				if index >= 4 {
					break
				}
				shown = append(shown, fmt.Sprintf("«%s» %s", truncateRunesText(item.Title, 60), shortEventTime(item.StartsAt)))
			}
			summary := fmt.Sprintf("Создать событий: %d — %s", len(args.Events), strings.Join(shown, ", "))
			if len(args.Events) > 4 {
				summary += fmt.Sprintf(" и ещё %d", len(args.Events)-4)
			}
			return summary + ".", nil
		},
	})
	registry.Register(Tool{
		Spec: assistantSpec("update_event", "Изменить существующее событие календаря. Отправляй только те поля, которые нужно поменять. Требует подтверждения.",
			objectSchema(map[string]any{
				"id":          integerProp("ID события"),
				"title":       stringProp("Новое название"),
				"starts_at":   stringProp("Новое начало, RFC3339"),
				"ends_at":     stringProp("Новое окончание, RFC3339"),
				"calendar":    enumProp("work", "meetings", "personal"),
				"location":    stringProp("Место"),
				"description": stringProp("Описание"),
				"all_day":     booleanProp("Событие на весь день"),
			}, "id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID          int64   `json:"id"`
				Title       *string `json:"title"`
				StartsAt    *string `json:"starts_at"`
				EndsAt      *string `json:"ends_at"`
				Calendar    *string `json:"calendar"`
				Location    *string `json:"location"`
				Description *string `json:"description"`
				AllDay      *bool   `json:"all_day"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			current, err := service.Get(ctx, args.ID)
			if err != nil {
				return nil, err
			}
			in := appevents.UpdateEventInput{
				Title:       args.Title,
				Description: args.Description,
				Location:    args.Location,
				Calendar:    args.Calendar,
				AllDay:      args.AllDay,
			}
			if args.StartsAt != nil {
				parsed, err := time.Parse(time.RFC3339, *args.StartsAt)
				if err != nil {
					return nil, fmt.Errorf("starts_at must be RFC3339")
				}
				in.StartsAt = &parsed
			}
			if args.EndsAt != nil {
				parsed, err := time.Parse(time.RFC3339, *args.EndsAt)
				if err != nil {
					return nil, fmt.Errorf("ends_at must be RFC3339")
				}
				in.EndsAt = &parsed
			}
			updated, err := service.Update(ctx, args.ID, current.Version, in)
			if err != nil {
				return nil, err
			}
			return eventView(updated), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID    int64   `json:"id"`
				Title *string `json:"title"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if args.Title != nil && strings.TrimSpace(*args.Title) != "" {
				return fmt.Sprintf("Изменить событие №%d на «%s»", args.ID, truncateRunesText(*args.Title, 60)), nil
			}
			return fmt.Sprintf("Изменить событие №%d", args.ID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("delete_event", "Удалить событие календаря безвозвратно. Восстановить его будет нельзя. Требует подтверждения.",
			objectSchema(map[string]any{
				"id": integerProp("ID события"),
			}, "id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			current, err := service.Get(ctx, args.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, args.ID, current.Version); err != nil {
				return nil, err
			}
			return map[string]any{"id": args.ID, "deleted": true, "title": current.Event.Title}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Удалить событие №%d безвозвратно", args.ID), nil
		},
	})
}

// shortEventTime renders an RFC3339 timestamp as a compact human date.
func shortEventTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.Format("02.01 15:04")
}

func eventDate(raw string, now time.Time, offsetDays int) (task.CalendarDate, error) {
	if raw == "" {
		value, _ := task.ParseCalendarDate(now.AddDate(0, 0, offsetDays).Format("2006-01-02"))
		return value, nil
	}
	value, err := task.ParseCalendarDate(raw)
	if err != nil {
		return task.CalendarDate{}, fmt.Errorf("invalid date %q", raw)
	}
	return value, nil
}

func eventView(item appevents.EventWithVersion) map[string]any {
	value := item.Event
	view := map[string]any{
		"id":        value.ID,
		"title":     value.Title,
		"calendar":  string(value.Calendar),
		"starts_at": value.StartsAt.Format(time.RFC3339),
		"ends_at":   value.EndsAt.Format(time.RFC3339),
		"all_day":   value.AllDay,
		"timezone":  value.Timezone,
		"version":   item.Version,
	}
	if value.Description != nil {
		view["description"] = *value.Description
	}
	if value.Location != nil {
		view["location"] = *value.Location
	}
	return view
}
