package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appassistant "github.com/theDenuvo/LifeOS/internal/application/assistant"
	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// Roadmap stage 7: the researcher and its external sources.
//
// The assistant researches on its own — searching, reading and comparing pages
// is a read-only act with a reversible result — but it never grants itself a
// new permission: a source the assistant connects starts disabled and
// read-only, and enabling it, or allowing outbound changes, is done by the user
// in the interface. Storing a research result in the knowledge base is
// immediate, like learning from a document: the entry can be edited and
// deleted, and pretending otherwise would bury the value of the research.

// researchService is the slice of the research service the tools need.
type researchService = appresearch.Service

// researchPermissionTools change what LifeOS may do with an external service.
// An automation must not run them: permissions are agreed with the user, not
// granted by a schedule.
var researchPermissionTools = map[string]bool{
	"add_external_source":    true,
	"update_external_source": true,
	"remove_external_source": true,
	"test_external_source":   true,
	// A call reaches an external service, and a source that allows writes
	// changes something outside LifeOS. An unattended schedule is not the
	// place for that decision.
	"call_external_source": true,
}

// RegisterResearchTools exposes research and its sources to the model.
//
// Like the automation tools it is registered after the service exists, because
// the research service owns no tools of its own: it is called by them.
func RegisterResearchTools(registry *Registry, service *researchService) {
	if service == nil {
		return
	}

	registry.Register(Tool{
		Spec: assistantSpec("web_search",
			"Найти страницы в интернете по запросу и вернуть заголовки, адреса и выдержки. Ничего не сохраняет: используй, чтобы понять, что стоит прочитать.",
			objectSchema(map[string]any{
				"query":     stringProp("Поисковый запрос"),
				"limit":     integerProp("Сколько результатов вернуть (1–20, по умолчанию 6)"),
				"source_id": integerProp("ID подключённого поискового источника; без него используется встроенный поиск"),
			}, "query")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Query    string `json:"query"`
				Limit    int    `json:"limit"`
				SourceID int64  `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			results, err := service.Search(ctx, args.Query, args.Limit, args.SourceID)
			if err != nil {
				return nil, researchToolError(err)
			}
			items := make([]map[string]any, 0, len(results))
			for _, result := range results {
				items = append(items, map[string]any{
					"title":   result.Title,
					"url":     result.URL,
					"site":    domainresearch.Host(result.URL),
					"snippet": result.Snippet,
				})
			}
			return map[string]any{"results": items, "count": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("read_web_page",
			"Прочитать страницу в интернете и вернуть её текст. Используй, когда пользователь назвал адрес или когда нужно проверить найденный результат.",
			objectSchema(map[string]any{
				"url": stringProp("Адрес страницы"),
			}, "url")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			page, err := service.Fetch(ctx, args.URL)
			if err != nil {
				return nil, researchToolError(err)
			}
			return map[string]any{
				"url":        page.URL,
				"title":      page.Title,
				"site":       domainresearch.Host(page.URL),
				"text":       truncateText(page.Text, readPageLimit),
				"characters": len([]rune(page.Text)),
				"truncated":  page.Truncated || len([]rune(page.Text)) > readPageLimit,
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("research_topic",
			"Провести исследование: найти источники, прочитать их, сопоставить и показать выводы с ссылками и уровнем достоверности. Вывод считается фактом только если его подтверждают минимум два независимых сайта; расхождения источников показываются отдельно. Результат сохраняется в базу знаний с указанием источников. Исследование выполняется в фоне: ответом придёт подтверждение, а готовый результат — сообщением в этот же диалог.",
			objectSchema(map[string]any{
				"question":        stringProp("Вопрос, на который нужен ответ"),
				"queries":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Поисковые запросы (до 3); без них используется сам вопрос"},
				"max_pages":       integerProp("Сколько страниц прочитать (1–8, по умолчанию 4)"),
				"save_knowledge":  map[string]any{"type": "boolean", "description": "Сохранить результат в базу знаний (по умолчанию да)"},
				"knowledge_title": stringProp("Название записи в базе знаний"),
				"source_id":       integerProp("ID подключённого поискового источника"),
			}, "question")),
		// Storing a report and a knowledge entry is reversible and is the point
		// of the capability, so it applies immediately.
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Question       string   `json:"question"`
				Queries        []string `json:"queries"`
				MaxPages       int      `json:"max_pages"`
				SaveKnowledge  *bool    `json:"save_knowledge"`
				KnowledgeTitle string   `json:"knowledge_title"`
				SourceID       int64    `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			save := true
			if args.SaveKnowledge != nil {
				save = *args.SaveKnowledge
			}
			input := appresearch.ResearchInput{
				Question:       args.Question,
				Queries:        args.Queries,
				MaxPages:       args.MaxPages,
				SaveKnowledge:  save,
				KnowledgeTitle: args.KnowledgeTitle,
				ConnectionID:   args.SourceID,
				ConversationID: conversationID(ctx),
			}
			if input.ConversationID == 0 {
				// Nobody is waiting in a chat — an automation captures the
				// tool result itself, and its run is not bound by the HTTP
				// write timeout, so the answer comes back in one piece.
				report, err := service.Research(ctx, input)
				if report != nil && err != nil {
					// The report exists with the failure recorded; the caller
					// still sees what happened instead of losing it.
					view := researchReportView(report)
					view["error"] = err.Error()
					return view, nil
				}
				if err != nil {
					return nil, researchToolError(err)
				}
				return researchReportView(report), nil
			}
			// In a conversation the run must outlive the turn that asked for
			// it: reading up to eight pages takes longer than an HTTP answer
			// may last. The report arrives in the conversation as a message
			// when it is done and stays readable in the research view.
			report, err := service.StartResearch(ctx, input)
			if err != nil {
				return nil, researchToolError(err)
			}
			view := researchReportView(report)
			view["status_detail"] = "Исследование выполняется в фоне. Готовый результат придёт в этот диалог сообщением и сохранится в разделе «Исследования»; отчёт можно открыть по номеру."
			return view, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Question string `json:"question"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Исследовать вопрос «%s» в интернете.", strings.TrimSpace(args.Question)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("list_research",
			"Список сохранённых исследований с вопросом, числом выводов и источников.",
			objectSchema(map[string]any{
				"search": stringProp("Поиск по вопросу и тексту итога"),
				"limit":  integerProp("Сколько вернуть (1–100, по умолчанию 20)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			reports, total, err := service.ListReports(ctx, appresearch.ReportFilter{Query: args.Search, Limit: args.Limit})
			if err != nil {
				return nil, researchToolError(err)
			}
			items := make([]map[string]any, 0, len(reports))
			for _, report := range reports {
				items = append(items, map[string]any{
					"id":           report.ID,
					"question":     report.Question,
					"status":       string(report.Status),
					"findings":     len(report.Findings),
					"sources":      report.Sources,
					"knowledge_id": report.KnowledgeEntryID,
					"started_at":   report.StartedAt.Format(time.RFC3339),
				})
			}
			return map[string]any{"research": items, "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("get_research",
			"Одно исследование целиком: итог, выводы с уровнем достоверности и ссылки на источники.",
			objectSchema(map[string]any{
				"id": integerProp("ID исследования"),
			}, "id")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID int64 `json:"id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			report, err := service.GetReport(ctx, args.ID)
			if err != nil {
				return nil, researchToolError(err)
			}
			return researchReportView(report), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("list_external_sources",
			"Подключённые внешние источники: тип, режим доступа, доверие и состояние.",
			objectSchema(nil)),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Kind string `json:"kind"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			filter := appresearch.ConnectionFilter{}
			if args.Kind != "" {
				kind := domainresearch.SourceKind(args.Kind)
				if !kind.IsValid() {
					return nil, fmt.Errorf("неизвестный тип источника %q", args.Kind)
				}
				filter.Kind = &kind
			}
			sources, err := service.ListConnections(ctx, filter)
			if err != nil {
				return nil, researchToolError(err)
			}
			items := make([]map[string]any, 0, len(sources))
			for _, source := range sources {
				items = append(items, sourceView(source))
			}
			return map[string]any{"sources": items, "count": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("add_external_source",
			"Подключить внешний источник: поиск, HTTP API, MCP-сервер или скрипт. Ассистент подключает источник только на чтение и в выключенном виде — включает его и разрешает отправку изменений пользователь в разделе «Исследования».",
			objectSchema(map[string]any{
				"name":     stringProp("Название источника"),
				"kind":     enumProp("web_search", "http_api", "mcp", "script"),
				"endpoint": stringProp("Адрес: шаблон поиска с {query}, базовый URL API, адрес MCP-сервера или команда скрипта"),
				"format":   enumProp("searxng", "duckduckgo"),
				"trust":    enumProp("high", "normal", "low"),
				"notes":    stringProp("Что это за источник и зачем он нужен"),
			}, "name", "kind", "endpoint")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Name     string `json:"name"`
				Kind     string `json:"kind"`
				Endpoint string `json:"endpoint"`
				Format   string `json:"format"`
				Trust    string `json:"trust"`
				Notes    string `json:"notes"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if domainresearch.SourceKind(args.Kind) == domainresearch.KindScript {
				return nil, fmt.Errorf("%w: попросите пользователя добавить источник-скрипт в разделе «Исследования»", appresearch.ErrScriptNeedsUser)
			}
			connection := &domainresearch.Connection{
				Name:               args.Name,
				Kind:               domainresearch.SourceKind(args.Kind),
				Mode:               domainresearch.ModeRead,
				Endpoint:           args.Endpoint,
				Format:             domainresearch.SearchFormat(args.Format),
				Trust:              domainresearch.Trust(args.Trust),
				Notes:              args.Notes,
				CreatedByAssistant: true,
			}
			if connection.Trust == "" {
				connection.Trust = domainresearch.TrustNormal
			}
			created, err := service.CreateConnection(ctx, connection)
			if err != nil {
				return nil, researchToolError(err)
			}
			result := sourceView(created)
			result["note"] = "Источник выключен: включите его в разделе «Исследования», чтобы ассистент мог им пользоваться"
			return result, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Name     string `json:"name"`
				Endpoint string `json:"endpoint"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Подключить внешний источник «%s» (%s).", strings.TrimSpace(args.Name), strings.TrimSpace(args.Endpoint)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_external_source",
			"Изменить описание источника. Режим доступа и включение меняет только пользователь в разделе «Исследования».",
			objectSchema(map[string]any{
				"source_id":        integerProp("ID источника"),
				"expected_version": integerProp("Версия из list_external_sources"),
				"name":             stringProp("Новое название"),
				"notes":            stringProp("Новое описание"),
				"trust":            enumProp("high", "normal", "low"),
			}, "source_id", "expected_version")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SourceID        int64   `json:"source_id"`
				ExpectedVersion int     `json:"expected_version"`
				Name            *string `json:"name"`
				Notes           *string `json:"notes"`
				Trust           *string `json:"trust"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			update := appresearch.ConnectionUpdate{Name: args.Name, Notes: args.Notes}
			if args.Trust != nil {
				trust := domainresearch.Trust(*args.Trust)
				update.Trust = &trust
			}
			updated, err := service.UpdateConnection(ctx, args.SourceID, args.ExpectedVersion, update)
			if err != nil {
				return nil, researchToolError(err)
			}
			return sourceView(updated), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				SourceID int64 `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Изменить описание внешнего источника #%d.", args.SourceID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("remove_external_source",
			"Удалить подключённый источник. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"source_id": integerProp("ID источника"),
			}, "source_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SourceID int64 `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := service.DeleteConnection(ctx, args.SourceID); err != nil {
				return nil, researchToolError(err)
			}
			return map[string]any{"status": "deleted", "source_id": args.SourceID}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				SourceID int64 `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Удалить внешний источник #%d вместе с подключением.", args.SourceID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("call_external_source",
			"Обратиться к подключённому источнику: путь HTTP API, инструмент MCP или команду скрипта. Источник только на чтение откажет в изменяющем запросе. Изменяющий вызов требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"source_id": integerProp("ID источника"),
				"operation": stringProp("Путь API (например /repos/owner/name), имя инструмента MCP или пусто для списка инструментов"),
				"method":    enumProp("GET", "POST", "PUT", "PATCH", "DELETE"),
				"body":      stringProp("Тело запроса или аргументы инструмента MCP в виде JSON"),
				"query":     stringProp("Строка запроса для поискового источника или аргумент скрипта"),
				"write":     booleanProp("Заявка на изменение вовне: нужна для инструмента MCP, который меняет данные. Источник только на чтение всё равно откажет"),
			}, "source_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SourceID  int64  `json:"source_id"`
				Operation string `json:"operation"`
				Method    string `json:"method"`
				Body      string `json:"body"`
				Query     string `json:"query"`
				Write     bool   `json:"write"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			method := strings.ToUpper(strings.TrimSpace(args.Method))
			// The HTTP method says what it can; an MCP tool has no method, so the
			// model must say that it means to change something, and the source
			// mode decides whether it may.
			write := args.Write || (method != "" && method != "GET" && method != "HEAD")
			result, err := service.CallSource(ctx, args.SourceID, appresearch.CallRequest{
				Operation: args.Operation,
				Method:    method,
				Body:      args.Body,
				Query:     args.Query,
				Write:     write,
			})
			if err != nil {
				return nil, researchToolError(err)
			}
			return map[string]any{
				"status":       result.Status,
				"content_type": result.ContentType,
				"body":         truncateText(result.Body, readPageLimit),
				"duration_ms":  result.Duration.Milliseconds(),
			}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				SourceID  int64  `json:"source_id"`
				Operation string `json:"operation"`
				Method    string `json:"method"`
				Write     bool   `json:"write"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			method := strings.ToUpper(strings.TrimSpace(args.Method))
			if method == "" {
				method = "GET"
			}
			if args.Write {
				return fmt.Sprintf("Изменить что-то вовне: %s %s на источнике #%d.", method,
					strings.TrimSpace(args.Operation), args.SourceID), nil
			}
			return fmt.Sprintf("Прочитать %s %s на внешнем источнике #%d.", method,
				strings.TrimSpace(args.Operation), args.SourceID), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("test_external_source",
			"Проверить подключённый источник: открыть список инструментов MCP или выполнить один запрос. У источника-скрипта это запуск команды, поэтому проверка выполняется только после подтверждения пользователя.",
			objectSchema(map[string]any{
				"source_id": integerProp("ID источника"),
			}, "source_id")),
		// A check is not reading: for a script source it runs the user's
		// command, and a command's effects are unknown to LifeOS. The call goes
		// through confirmation like any other action with consequences.
		ReadOnly: false,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SourceID int64 `json:"source_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			source, err := service.GetConnection(ctx, args.SourceID)
			if err != nil {
				return nil, researchToolError(err)
			}
			request := appresearch.CallRequest{Write: false}
			switch source.Kind {
			case domainresearch.KindWebSearch:
				request.Query = "LifeOS"
			case domainresearch.KindMCP:
				request.Operation = "tools/list"
			default:
				request.Method = "GET"
			}
			result, err := service.CallSource(ctx, args.SourceID, request)
			if err != nil {
				return nil, researchToolError(err)
			}
			return map[string]any{
				"source_id": source.ID,
				"status":    result.Status,
				"body":      truncateText(result.Body, readPageLimit),
			}, nil
		},
	})

	registerWatchTools(registry, service)
}

// registerWatchTools exposes the standing questions.
func registerWatchTools(registry *Registry, service *researchService) {
	registry.Register(Tool{
		Spec: assistantSpec("list_watches",
			"Наблюдения за темами: что отслеживается, когда проверялось и что изменилось.",
			objectSchema(map[string]any{
				"include_disabled": map[string]any{"type": "boolean", "description": "Показать и выключенные наблюдения"},
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				IncludeDisabled bool `json:"include_disabled"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			watches, err := service.ListWatches(ctx, args.IncludeDisabled)
			if err != nil {
				return nil, researchToolError(err)
			}
			items := make([]map[string]any, 0, len(watches))
			for _, watch := range watches {
				items = append(items, watchView(watch))
			}
			return map[string]any{"watches": items, "count": len(items)}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_watch",
			"Наблюдать за темой: LifeOS будет периодически перепроверять её в интернете и сообщать, что изменилось. Это наблюдение, а не действие: ничего не меняется само.",
			objectSchema(map[string]any{
				"topic":   stringProp("Что отслеживать, по-человечески"),
				"query":   stringProp("Поисковый запрос наблюдения"),
				"queries": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Дополнительные запросы (до 3)"},
				"cadence": enumProp("daily", "weekly", "manual"),
			}, "topic", "query")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Topic   string   `json:"topic"`
				Query   string   `json:"query"`
				Queries []string `json:"queries"`
				Cadence string   `json:"cadence"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			cadence := domainresearch.Cadence(args.Cadence)
			if cadence == "" {
				cadence = domainresearch.CadenceDaily
			}
			watch, err := service.CreateWatch(ctx, appresearch.WatchInput{
				Topic: args.Topic, Query: args.Query, Queries: args.Queries, Cadence: cadence,
			})
			if err != nil {
				return nil, researchToolError(err)
			}
			return watchView(watch), nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Topic string `json:"topic"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Наблюдать за темой «%s».", strings.TrimSpace(args.Topic)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("check_watch",
			"Перепроверить наблюдение прямо сейчас и вернуть, что изменилось с прошлого раза.",
			objectSchema(map[string]any{
				"watch_id": integerProp("ID наблюдения"),
			}, "watch_id")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				WatchID int64 `json:"watch_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			watch, diff, err := service.CheckWatch(ctx, args.WatchID)
			if err != nil {
				return nil, researchToolError(err)
			}
			result := watchView(watch)
			if diff != nil {
				result["change"] = map[string]any{
					"summary":   diff.Summary(),
					"new":       fingerprintViews(diff.New),
					"updated":   fingerprintViews(diff.Updated),
					"unchanged": diff.Unchanged,
				}
			}
			return result, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("update_watch",
			"Изменить наблюдение: тему, запрос или периодичность, включить или выключить.",
			objectSchema(map[string]any{
				"watch_id":         integerProp("ID наблюдения"),
				"expected_version": integerProp("Версия из list_watches"),
				"topic":            stringProp("Новая тема"),
				"query":            stringProp("Новый запрос"),
				"cadence":          enumProp("daily", "weekly", "manual"),
				"enabled":          map[string]any{"type": "boolean", "description": "Включить или выключить наблюдение"},
			}, "watch_id", "expected_version")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				WatchID         int64   `json:"watch_id"`
				ExpectedVersion int     `json:"expected_version"`
				Topic           *string `json:"topic"`
				Query           *string `json:"query"`
				Cadence         *string `json:"cadence"`
				Enabled         *bool   `json:"enabled"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			update := appresearch.WatchUpdate{Topic: args.Topic, Query: args.Query, Enabled: args.Enabled}
			if args.Cadence != nil {
				cadence := domainresearch.Cadence(*args.Cadence)
				update.Cadence = &cadence
			}
			watch, err := service.UpdateWatch(ctx, args.WatchID, args.ExpectedVersion, update)
			if err != nil {
				return nil, researchToolError(err)
			}
			return watchView(watch), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("delete_watch",
			"Прекратить наблюдение за темой. Требует подтверждения пользователя.",
			objectSchema(map[string]any{
				"watch_id": integerProp("ID наблюдения"),
			}, "watch_id")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				WatchID int64 `json:"watch_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := service.DeleteWatch(ctx, args.WatchID); err != nil {
				return nil, researchToolError(err)
			}
			return map[string]any{"status": "deleted", "watch_id": args.WatchID}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				WatchID int64 `json:"watch_id"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Прекратить наблюдение #%d.", args.WatchID), nil
		},
	})
}

// readPageLimit bounds how much page text reaches the model in one answer; a
// longer page is still readable through several calls.
const readPageLimit = 12000

// conversationID reads the conversation the tool was called from, if any: the
// result of the background run is delivered there.
func conversationID(ctx context.Context) int64 {
	id, _ := appassistant.ToolConversation(ctx)
	return id
}

func researchReportView(report *domainresearch.Report) map[string]any {
	if report == nil {
		return map[string]any{}
	}
	findings := make([]map[string]any, 0, len(report.Findings))
	for _, finding := range report.Findings {
		citations := make([]map[string]any, 0, len(finding.Citations))
		for _, citation := range finding.Citations {
			citations = append(citations, map[string]any{
				"url":     citation.URL,
				"title":   citation.Title,
				"site":    citation.Site,
				"trust":   string(citation.Trust),
				"excerpt": citation.Excerpt,
			})
		}
		item := map[string]any{
			"statement":     finding.Statement,
			"certainty":     string(finding.Certainty),
			"confidence":    finding.Confidence,
			"corroboration": finding.Corroboration,
			"domains":       finding.Domains,
			"citations":     citations,
		}
		if finding.Conflict != "" {
			item["conflict"] = finding.Conflict
		}
		findings = append(findings, item)
	}
	body := map[string]any{
		"id":         report.ID,
		"question":   report.Question,
		"queries":    report.Queries,
		"status":     string(report.Status),
		"summary":    truncateText(report.Summary, 20000),
		"findings":   findings,
		"sources":    report.Sources,
		"started_at": report.StartedAt.Format(time.RFC3339),
		"version":    report.Version,
	}
	if report.FinishedAt != nil {
		body["finished_at"] = report.FinishedAt.Format(time.RFC3339)
	}
	if report.KnowledgeEntryID != nil {
		body["knowledge_id"] = *report.KnowledgeEntryID
	}
	if report.Error != "" {
		body["error"] = report.Error
	}
	body["certainty_rule"] = "факт — подтверждён минимум двумя независимыми сайтами; предположение — один источник; расхождение показывается отдельно"
	return body
}

func sourceView(source *domainresearch.Connection) map[string]any {
	body := map[string]any{
		"id":       source.ID,
		"name":     source.Name,
		"kind":     string(source.Kind),
		"mode":     string(source.Mode),
		"trust":    string(source.Trust),
		"endpoint": source.Endpoint,
		"notes":    source.Notes,
		"enabled":  source.Enabled,
		"version":  source.Version,
	}
	if source.Format != "" {
		body["format"] = string(source.Format)
	}
	if source.LastUsedAt != nil {
		body["last_used_at"] = source.LastUsedAt.Format(time.RFC3339)
	}
	return body
}

func watchView(watch *domainresearch.Watch) map[string]any {
	body := map[string]any{
		"id":                  watch.ID,
		"topic":               watch.Topic,
		"query":               watch.Query,
		"queries":             watch.Queries,
		"cadence":             string(watch.Cadence),
		"enabled":             watch.Enabled,
		"version":             watch.Version,
		"last_change_summary": watch.LastChangeSummary,
	}
	if watch.NextCheckAt != nil {
		body["next_check_at"] = watch.NextCheckAt.Format(time.RFC3339)
	}
	if watch.LastCheckedAt != nil {
		body["last_checked_at"] = watch.LastCheckedAt.Format(time.RFC3339)
	}
	if watch.LastChangeAt != nil {
		body["last_change_at"] = watch.LastChangeAt.Format(time.RFC3339)
	}
	return body
}

func fingerprintViews(items []domainresearch.Fingerprint) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"url":   item.URL,
			"title": item.Title,
		})
	}
	return result
}

// researchToolError keeps service errors readable in chat.
func researchToolError(err error) error {
	switch {
	case errors.Is(err, appresearch.ErrNotFound):
		return fmt.Errorf("исследование не найдено")
	case errors.Is(err, appresearch.ErrSourceNotFound):
		return fmt.Errorf("внешний источник не найден")
	case errors.Is(err, appresearch.ErrWatchNotFound):
		return fmt.Errorf("наблюдение не найдено")
	case errors.Is(err, appresearch.ErrVersionConflict):
		return fmt.Errorf("запись изменена кем-то ещё; перечитай её и повтори с актуальной версией")
	case errors.Is(err, appresearch.ErrSourceDisabled):
		return fmt.Errorf("%w — включите его в разделе «Исследования»", err)
	case errors.Is(err, appresearch.ErrSourceReadOnly):
		return fmt.Errorf("%w: изменение внешнего сервиса не разрешено", err)
	case errors.Is(err, appresearch.ErrNoProvider):
		return fmt.Errorf("поиск в интернете не настроен: задайте LIFEOS_SEARCH_URL или подключите поисковый источник")
	case errors.Is(err, appresearch.ErrScriptNeedsUser):
		return err
	}
	return err
}
