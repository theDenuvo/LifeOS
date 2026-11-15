package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

// registerMemoryTools exposes the durable memory and knowledge base. Reads run
// immediately; writes stay pending until the user confirms them, except memory
// learning which the roadmap allows to run autonomously through LearnConversation.
func registerMemoryTools(registry *Registry, service *appmemory.Service) {
	registry.Register(Tool{
		Spec: assistantSpec("search_memory",
			"Поиск по долговременной памяти и базе знаний: предпочтения, привычки, обстоятельства, договорённости, инструкции, конспекты и материалы. Используй перед тем, как спрашивать пользователя о том, что он уже сообщал.",
			objectSchema(map[string]any{
				"query":    stringProp("Поисковый запрос"),
				"area":     enumProp("personal", "knowledge"),
				"category": stringProp("Категория памяти (необязательно)"),
				"limit":    integerProp("Сколько записей вернуть (1-50)"),
			}, "query")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Query    string `json:"query"`
				Area     string `json:"area"`
				Category string `json:"category"`
				Limit    int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if strings.TrimSpace(args.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			filter := appmemory.Filter{Query: args.Query, Limit: args.Limit}
			if args.Area != "" {
				area := domainmemory.Area(args.Area)
				if area != domainmemory.AreaPersonal && area != domainmemory.AreaKnowledge {
					return nil, fmt.Errorf("invalid area %q", args.Area)
				}
				filter.Area = &area
			}
			if args.Category != "" {
				category := domainmemory.Category(args.Category)
				if !category.IsValid() {
					return nil, fmt.Errorf("invalid category %q", args.Category)
				}
				filter.Category = &category
			}
			entries, total, err := service.List(ctx, filter)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entries": memoryViews(entries, 4), "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("list_memory",
			"Просмотр памяти без запроса: личные сведения и материалы знаний.",
			objectSchema(map[string]any{
				"area":  enumProp("personal", "knowledge"),
				"limit": integerProp("Сколько записей вернуть (1-50)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Area  string `json:"area"`
				Limit int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			filter := appmemory.Filter{Limit: args.Limit}
			if args.Area != "" {
				area := domainmemory.Area(args.Area)
				if area != domainmemory.AreaPersonal && area != domainmemory.AreaKnowledge {
					return nil, fmt.Errorf("invalid area %q", args.Area)
				}
				filter.Area = &area
			}
			entries, total, err := service.List(ctx, filter)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entries": memoryViews(entries, 3), "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("remember",
			"Сохранить в долговременную память устойчивое сведение о пользователе или его мире. Требует подтверждения пользователя. Не используй для разовых задач и планов.",
			objectSchema(map[string]any{
				"title":      stringProp("Краткая тема, 1-8 слов"),
				"content":    stringProp("Одно ясное утверждение"),
				"category":   enumProp("preference", "habit", "circumstance", "fact", "agreement", "context", "rule", "instruction", "reference", "research", "idea", "note"),
				"certainty":  enumProp("fact", "assumption", "inference"),
				"confidence": map[string]any{"type": "number", "description": "Уверенность 0-1"},
				"entities":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Связанные имена, проекты, места"},
				"source":     stringProp("Откуда это известно: слова пользователя, документ, наблюдение"),
			}, "title", "content", "category")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title      string   `json:"title"`
				Content    string   `json:"content"`
				Category   string   `json:"category"`
				Certainty  string   `json:"certainty"`
				Confidence *float64 `json:"confidence"`
				Entities   []string `json:"entities"`
				Source     string   `json:"source"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			category := domainmemory.Category(args.Category)
			if !category.IsValid() {
				return nil, fmt.Errorf("invalid category %q", args.Category)
			}
			certainty := domainmemory.Certainty(strings.TrimSpace(args.Certainty))
			if certainty == "" {
				certainty = domainmemory.CertaintyFact
			}
			if !certainty.IsValid() {
				return nil, fmt.Errorf("invalid certainty %q", args.Certainty)
			}
			source := memorySource(args.Source, args.Content)
			result, err := service.Remember(ctx, appmemory.RememberInput{
				CreateInput: appmemory.CreateInput{
					Title: args.Title, Content: args.Content, Category: category,
					Certainty: certainty, Confidence: args.Confidence, Entities: args.Entities,
					Source: source,
				},
				ReplaceSameSubject: certainty == domainmemory.CertaintyFact,
			})
			if err != nil {
				return nil, err
			}
			out := map[string]any{"entry": memoryView(result.Entry, 3), "duplicate": result.Duplicate}
			if result.Conflict != nil {
				out["conflict"] = "Сведение отличается от сохранённого ранее и ждёт решения пользователя в разделе «Память»."
			}
			return out, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Content) != "" {
				return fmt.Sprintf("Запомнить: %s — «%s»", args.Title, truncateText(args.Content, 120)), nil
			}
			return fmt.Sprintf("Запомнить «%s»", args.Title), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("save_knowledge",
			"Сохранить материал в базу знаний: инструкцию, конспект, справку, исследование или идею. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"title":     stringProp("Название материала"),
				"content":   stringProp("Содержимое в Markdown"),
				"category":  enumProp("instruction", "summary", "reference", "research", "idea", "documentation", "note"),
				"entities":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"certainty": enumProp("fact", "assumption", "inference"),
				"source":    stringProp("Источник материала"),
			}, "title", "content", "category")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title     string   `json:"title"`
				Content   string   `json:"content"`
				Category  string   `json:"category"`
				Entities  []string `json:"entities"`
				Certainty string   `json:"certainty"`
				Source    string   `json:"source"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			category := domainmemory.Category(args.Category)
			if !category.IsValid() || category.Area() != domainmemory.AreaKnowledge {
				return nil, fmt.Errorf("category %q is not a knowledge category", args.Category)
			}
			certainty := domainmemory.Certainty(strings.TrimSpace(args.Certainty))
			if certainty == "" {
				certainty = domainmemory.CertaintyFact
			}
			if !certainty.IsValid() {
				return nil, fmt.Errorf("invalid certainty %q", args.Certainty)
			}
			result, err := service.Remember(ctx, appmemory.RememberInput{
				CreateInput: appmemory.CreateInput{
					Title: args.Title, Content: args.Content, Category: category,
					Certainty: certainty, Entities: args.Entities,
					Source: memorySource(args.Source, truncateText(args.Content, 1000)),
				},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"entry": memoryView(result.Entry, 3), "duplicate": result.Duplicate}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Title string `json:"title"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Сохранить в базу знаний «%s»", args.Title), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_memory",
			"Исправить сведение в памяти. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"id":        integerProp("ID записи памяти"),
				"version":   integerProp("Версия записи из последнего чтения"),
				"title":     stringProp("Новая тема"),
				"content":   stringProp("Новое содержание"),
				"certainty": enumProp("fact", "assumption", "inference"),
				"entities":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "id", "version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID        int64    `json:"id"`
				Version   int      `json:"version"`
				Title     *string  `json:"title"`
				Content   *string  `json:"content"`
				Certainty *string  `json:"certainty"`
				Entities  []string `json:"entities"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			input := appmemory.UpdateInput{Title: args.Title, Content: args.Content}
			if args.Certainty != nil {
				certainty := domainmemory.Certainty(*args.Certainty)
				if !certainty.IsValid() {
					return nil, fmt.Errorf("invalid certainty %q", *args.Certainty)
				}
				input.Certainty = &certainty
			}
			if args.Entities != nil {
				input.Entities = &args.Entities
			}
			entry, err := service.Update(ctx, args.ID, args.Version, input)
			if err != nil {
				return nil, err
			}
			return memoryView(entry, 3), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Исправить запись памяти #%d", args.ID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("forget_memory",
			"Удалить сведение из памяти. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"id":      integerProp("ID записи памяти"),
				"version": integerProp("Версия записи из последнего чтения"),
			}, "id", "version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID      int64 `json:"id"`
				Version int   `json:"version"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, args.ID, args.Version); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": args.ID}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Удалить запись памяти #%d", args.ID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("link_memory",
			"Связать запись памяти или материала знаний с задачей, событием, диалогом или документом. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"entry_id":    integerProp("ID записи памяти"),
				"target_type": enumProp("task", "event", "conversation", "document", "memory", "inbox", "goal"),
				"target_id":   integerProp("ID объекта"),
				"relation":    stringProp("Смысл связи, например «относится к», «источник», «продолжение»"),
			}, "entry_id", "target_type", "target_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				EntryID    int64  `json:"entry_id"`
				TargetType string `json:"target_type"`
				TargetID   int64  `json:"target_id"`
				Relation   string `json:"relation"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			targetType := domainmemory.TargetType(args.TargetType)
			if !targetType.IsValid() {
				return nil, fmt.Errorf("invalid target_type %q", args.TargetType)
			}
			relation := strings.TrimSpace(args.Relation)
			if relation == "" {
				relation = "относится к"
			}
			link, err := service.Link(ctx, &domainmemory.Link{
				EntryID: args.EntryID, TargetType: targetType, TargetID: args.TargetID, Relation: relation,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"link": linkView(*link)}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				EntryID    int64  `json:"entry_id"`
				TargetType string `json:"target_type"`
				TargetID   int64  `json:"target_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Связать память #%d с %s #%d", args.EntryID, args.TargetType, args.TargetID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("memory_conflicts",
			"Неразрешённые противоречия в памяти: система не уверена, какое из сведений верно.",
			objectSchema(nil)),
		ReadOnly: true,
		Execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
			views, err := service.Conflicts(ctx, true)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(views))
			for _, view := range views {
				items = append(items, map[string]any{
					"conflict_id": view.Conflict.ID,
					"reason":      view.Conflict.Reason,
					"existing":    memoryView(view.Existing, 2),
					"candidate":   memoryView(view.Candidate, 2),
				})
			}
			return map[string]any{"conflicts": items, "total": len(items)}, nil
		},
	})
}

// memorySource records who asserted the claim. Without an explicit source the
// statement came from the user, even though the assistant stored it.
func memorySource(label, excerpt string) domainmemory.Source {
	kind := domainmemory.SourceUser
	name := "Слова пользователя"
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		kind = domainmemory.SourceAssistant
		name = truncateText(trimmed, 500)
	}
	return domainmemory.Source{Kind: kind, Label: name, Excerpt: excerpt}
}

func memoryView(entry *domainmemory.Entry, sources int) map[string]any {
	if entry == nil {
		return nil
	}
	view := map[string]any{
		"id":         entry.ID,
		"area":       string(entry.Area()),
		"category":   string(entry.Category),
		"title":      entry.Title,
		"content":    entry.Content,
		"certainty":  string(entry.Certainty),
		"confidence": entry.Confidence,
		"status":     string(entry.Status),
		"entities":   entry.Entities,
		"version":    entry.Version,
		"updated_at": entry.UpdatedAt.Format(time.RFC3339),
	}
	if entry.SupersededBy != nil {
		view["superseded_by"] = *entry.SupersededBy
	}
	if sources > 0 && len(entry.Sources) > 0 {
		provenance := make([]map[string]any, 0, min(sources, len(entry.Sources)))
		for i, source := range entry.Sources {
			if i >= sources {
				break
			}
			provenance = append(provenance, map[string]any{
				"kind":    string(source.Kind),
				"label":   source.Label,
				"excerpt": source.Excerpt,
				"at":      source.ObservedAt.Format(time.RFC3339),
			})
		}
		view["sources"] = provenance
	}
	return view
}

func memoryViews(entries []*domainmemory.Entry, sources int) []map[string]any {
	views := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		views = append(views, memoryView(entry, sources))
	}
	return views
}

func linkView(link domainmemory.Link) map[string]any {
	return map[string]any{
		"id": link.ID, "entry_id": link.EntryID, "target_type": string(link.TargetType),
		"target_id": link.TargetID, "relation": link.Relation,
	}
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// ensure the assistant package import stays meaningful for tool specs.
var _ = assistant.ToolSpec{}
