package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// Roadmap stage 3: the main chat and project workspaces.
//
// The main chat stays the everyday surface. A long topic can be routed into a
// project workspace: a separate conversation with its own history and purpose
// that shares the LifeOS memory and tools. Significant results travel back to
// the main chat through report_result, so nothing has to be copied by hand.

// WorkspaceRunner runs one assistant turn inside a workspace conversation.
type WorkspaceRunner interface {
	SendMessageWithInteraction(ctx context.Context, conversationID int64, content string, interaction assistant.InteractionContext) (assistant.SendResult, error)
}

// WorkspaceHub connects the workspace tools to the assistant runtime. The
// registry is built before the assistant service, so the runner is bound
// afterwards; until then delegation reports itself as unavailable.
type WorkspaceHub struct {
	uow       assistant.UnitOfWork
	publisher events.Publisher
	clock     func() time.Time

	mu     sync.Mutex
	runner WorkspaceRunner
}

// NewWorkspaceHub builds the hub over the assistant unit of work.
func NewWorkspaceHub(uow assistant.UnitOfWork, publisher events.Publisher) *WorkspaceHub {
	return &WorkspaceHub{
		uow:       uow,
		publisher: publisher,
		clock:     func() time.Time { return time.Now().UTC().Truncate(time.Second) },
	}
}

// Bind attaches the assistant runtime used to run delegated turns.
func (h *WorkspaceHub) Bind(runner WorkspaceRunner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runner = runner
}

func (h *WorkspaceHub) boundRunner() WorkspaceRunner {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runner
}

// registerWorkspaceTools exposes workspace routing to the model. It is a
// no-op without a hub: a deployment without the assistant database still
// keeps every other tool.
func registerConversationTools(registry *Registry, hub *WorkspaceHub) {
	if hub == nil {
		return
	}

	registry.Register(Tool{
		Spec: assistantSpec("list_workspaces", "Открытые проектные рабочие пространства: отдельные чаты по конкретным темам. Используй, чтобы выбрать пространство для продолжения работы.",
			objectSchema(map[string]any{
				"limit": integerProp("Сколько пространств вернуть (1-20, по умолчанию 10)"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Limit int `json:"limit"`
			}
			_ = decodeArgs(raw, &args)
			limit := args.Limit
			if limit <= 0 {
				limit = 10
			}
			if limit > 20 {
				limit = 20
			}
			project := domainassistant.KindProject
			workspaces, total, err := listWorkspaces(ctx, hub, limit, &project)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(workspaces))
			for _, workspace := range workspaces {
				items = append(items, workspaceView(workspace))
			}
			return map[string]any{"workspaces": items, "total": total}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("create_workspace", "Создать отдельное рабочее пространство для продолжительной темы: проектный чат со своим контекстом и историей. Пространство использует общую память LifeOS.",
			objectSchema(map[string]any{
				"title":   stringProp("Короткое название темы"),
				"purpose": stringProp("Что будет разбираться в этом пространстве"),
			}, "title")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Title   string `json:"title"`
				Purpose string `json:"purpose"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			source := sourceConversationID(ctx)
			conversation, err := hub.create(ctx, args.Title, args.Purpose, source)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"workspace_id": conversation.ID,
				"title":        derefString(conversation.Title),
				"purpose":      derefString(conversation.Purpose),
				"note":         "Пространство создано. Чтобы передать туда работу, вызови delegate_to_workspace с workspace_id и поручением.",
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("delegate_to_workspace", "Передать работу в проектное пространство: отправить туда поручение, дождаться ответа и вернуть его сюда. Если пространства с таким названием нет, оно будет создано.",
			objectSchema(map[string]any{
				"workspace_id": integerProp("ID существующего пространства (см. list_workspaces)"),
				"title":        stringProp("Название пространства: существующего или нового"),
				"task":         stringProp("Поручение для пространства: что нужно разобрать и в каком состоянии передаётся работа"),
			}, "task")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if assistant.ToolContextFrom(ctx).Depth > 0 {
				return nil, fmt.Errorf("делегирование недоступно внутри рабочего пространства")
			}
			var args struct {
				WorkspaceID int64  `json:"workspace_id"`
				Title       string `json:"title"`
				Task        string `json:"task"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if args.WorkspaceID == 0 && strings.TrimSpace(args.Title) == "" {
				return nil, fmt.Errorf("укажи workspace_id или title пространства")
			}
			runner := hub.boundRunner()
			if runner == nil {
				return nil, fmt.Errorf("ассистент недоступен, передача в пространство сейчас невозможна")
			}

			workspace, created, err := hub.ensure(ctx, args.WorkspaceID, strings.TrimSpace(args.Title), args.Task, sourceConversationID(ctx))
			if err != nil {
				return nil, err
			}
			sendCtx := assistant.WithToolDepth(ctx, assistant.ToolContextFrom(ctx).Depth+1)
			result, err := runner.SendMessageWithInteraction(sendCtx, workspace.ID, args.Task, assistant.InteractionContext{})
			if err != nil {
				return nil, fmt.Errorf("пространство не ответило: %w", err)
			}
			response := map[string]any{
				"workspace_id": workspace.ID,
				"title":        derefString(workspace.Title),
				"created":      created,
				"reply":        result.Message.Content,
			}
			if created {
				response["note"] = "Пространство создано и получило поручение. Оно появится в списке пространств интерфейса."
			}
			return response, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("report_result", "Вернуть существенный результат этого рабочего пространства в главный чат: краткий итог, решение или статус. Главное пространство увидит его автоматически.",
			objectSchema(map[string]any{
				"summary": stringProp("Итог работы: что сделано, какое решение принято, что дальше"),
			}, "summary")),
		Direct: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			toolContext := assistant.ToolContextFrom(ctx)
			if toolContext.ConversationID == 0 {
				return nil, fmt.Errorf("report_result доступен только внутри рабочего пространства")
			}
			var args struct {
				Summary string `json:"summary"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary обязателен")
			}
			delivered, err := hub.report(ctx, toolContext.ConversationID, summary)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"delivered":         delivered,
				"main_conversation": delivered,
				"note":              "Результат передан в главный чат.",
			}, nil
		},
	})
}

// listWorkspaces returns open project conversations.
func listWorkspaces(ctx context.Context, hub *WorkspaceHub, limit int, kind *domainassistant.Kind) ([]*domainassistant.Conversation, int, error) {
	var (
		workspaces []*domainassistant.Conversation
		total      int
	)
	err := hub.uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		found, count, err := tx.Assistant().ListConversations(ctx, limit, 0, kind)
		if err != nil {
			return err
		}
		workspaces = found
		total = count
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list workspaces: %w", err)
	}
	return workspaces, total, nil
}

// create stores a new project conversation.
func (h *WorkspaceHub) create(ctx context.Context, title, purpose string, sourceID *int64) (*domainassistant.Conversation, error) {
	conversation, err := domainassistant.NewWorkspace(title, purpose, sourceID, h.clock())
	if err != nil {
		return nil, err
	}
	err = h.uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		id, err := tx.Assistant().CreateConversation(ctx, conversation)
		if err != nil {
			return err
		}
		conversation.ID = id
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	h.publish("assistant.workspace", map[string]any{
		"conversation_id": conversation.ID,
		"title":           derefString(conversation.Title),
	})
	return conversation, nil
}

// ensure resolves the target workspace for a delegation. An explicit ID must
// point at a project conversation; a title is matched against open
// workspaces and a miss creates a new one carrying the task as its purpose.
func (h *WorkspaceHub) ensure(ctx context.Context, id int64, title, task string, sourceID *int64) (*domainassistant.Conversation, bool, error) {
	if id > 0 {
		var workspace *domainassistant.Conversation
		err := h.uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
			found, err := tx.Assistant().GetConversation(ctx, id)
			if err != nil {
				return err
			}
			workspace = found
			return nil
		})
		if err != nil {
			return nil, false, fmt.Errorf("workspace %d: %w", id, err)
		}
		if workspace.Kind != domainassistant.KindProject {
			return nil, false, fmt.Errorf("диалог %d не является рабочим пространством", id)
		}
		return workspace, false, nil
	}

	project := domainassistant.KindProject
	workspaces, _, err := listWorkspaces(ctx, h, 20, &project)
	if err != nil {
		return nil, false, err
	}
	needle := strings.ToLower(title)
	for _, workspace := range workspaces {
		if workspace.Title != nil && strings.ToLower(strings.TrimSpace(*workspace.Title)) == needle {
			return workspace, false, nil
		}
	}
	purpose := task
	created, err := h.create(ctx, title, purpose, sourceID)
	if err != nil {
		return nil, false, err
	}
	return created, true, nil
}

// report copies a workspace's significant result into the main conversation.
func (h *WorkspaceHub) report(ctx context.Context, workspaceID int64, summary string) (bool, error) {
	var (
		workspace *domainassistant.Conversation
		mainID    int64
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		found, err := tx.Assistant().GetConversation(ctx, workspaceID)
		if err != nil {
			return err
		}
		workspace = found
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("workspace %d: %w", workspaceID, err)
	}
	if workspace.Kind != domainassistant.KindProject {
		return false, fmt.Errorf("report_result доступен только внутри рабочего пространства")
	}
	if workspace.SourceConversationID == nil {
		return false, fmt.Errorf("у этого пространства нет главного чата для передачи результата")
	}
	mainID = *workspace.SourceConversationID

	title := "рабочее пространство"
	if workspace.Title != nil && strings.TrimSpace(*workspace.Title) != "" {
		title = "«" + *workspace.Title + "»"
	}
	content := "Результат из рабочего пространства " + title + ":\n\n" + summary
	now := h.clock()
	message, err := domainassistant.NewMessage(mainID, domainassistant.RoleAssistant, content, now)
	if err != nil {
		return false, fmt.Errorf("handoff message: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"handoff": map[string]any{
			"workspace_id": workspaceID,
			"title":        derefString(workspace.Title),
		},
	})
	if err == nil {
		message.Metadata = metadata
	}

	err = h.uow.Do(ctx, func(ctx context.Context, tx assistant.Transaction) error {
		if _, err := tx.Assistant().InsertMessage(ctx, message); err != nil {
			return err
		}
		return tx.Assistant().TouchConversation(ctx, mainID, now)
	})
	if err != nil {
		return false, fmt.Errorf("handoff message: %w", err)
	}

	h.publish("assistant.message", map[string]any{
		"conversation_id": mainID,
		"message_id":      message.ID,
		"role":            string(domainassistant.RoleAssistant),
		"content":         content,
		"handoff":         true,
	})
	slog.Info("workspace result handed off", "workspace_id", workspaceID, "main_conversation_id", mainID)
	return true, nil
}

func (h *WorkspaceHub) publish(eventType string, data any) {
	if h.publisher == nil {
		return
	}
	h.publisher.Publish(eventType, data)
}

// sourceConversationID is the main chat a delegated call came from.
func sourceConversationID(ctx context.Context) *int64 {
	toolContext := assistant.ToolContextFrom(ctx)
	if toolContext.ConversationID == 0 {
		return nil
	}
	id := toolContext.ConversationID
	return &id
}

func workspaceView(conversation *domainassistant.Conversation) map[string]any {
	return map[string]any{
		"id":         conversation.ID,
		"title":      derefString(conversation.Title),
		"purpose":    derefString(conversation.Purpose),
		"updated_at": conversation.UpdatedAt.Format(time.RFC3339),
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
