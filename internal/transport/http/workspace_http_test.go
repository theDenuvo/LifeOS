package httptransport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/agent/tools"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/events"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

// newWorkspaceAPI builds a full assistant stack with workspace routing bound,
// over a temporary database.
func newWorkspaceAPI(t *testing.T, provider assistant.Provider) (*Handler, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := sqlite.Open(tmpDir + "/api.db")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := sqlite.Migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clock := apiClock{now: now}
	broker := events.NewBroker()
	hub := tools.NewWorkspaceHub(sqlite.NewAssistantUnitOfWork(db), broker)
	registry := tools.NewLifeOSTools(nil, nil, nil, tools.WithWorkspaceHub(hub))
	assistantService := assistant.NewService(
		sqlite.NewAssistantUnitOfWork(db),
		provider,
		registry,
		clock,
		assistant.WithTimezone("Europe/Moscow"),
		assistant.WithPublisher(broker),
	)
	hub.Bind(assistantService)

	handler := NewHandler(db, &config.Config{DataDir: tmpDir, Timezone: "Europe/Moscow"}, testEmbedFS, Services{
		Assistant: assistantService,
		Broker:    broker,
	})
	handler.now = func() time.Time { return now }
	return handler, func() { _ = db.Close() }
}

func TestCreateWorkspaceEndpoint(t *testing.T) {
	handler, cleanup := newWorkspaceAPI(t, &httpScriptedProvider{})
	defer cleanup()

	recorder, workspace := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/workspaces", map[string]any{
		"title":   "Ремонт",
		"purpose": "смета и подрядчики",
	}, nil)
	requireStatus(t, recorder, http.StatusCreated)
	if workspace["kind"] != "project" {
		t.Fatalf("kind = %v", workspace["kind"])
	}
	id := int64(workspace["id"].(float64))

	recorder, list := apiRequest(t, handler, http.MethodGet, "/api/v1/assistant/conversations?kind=project", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	items := list["data"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"].(float64) != float64(id) {
		t.Fatalf("workspace list = %v", list["data"])
	}

	recorder, mains := apiRequest(t, handler, http.MethodGet, "/api/v1/assistant/conversations?kind=main", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if len(mains["data"].([]any)) != 0 {
		t.Fatalf("main list must be empty, got %v", mains["data"])
	}

	recorder, view := apiRequest(t, handler, http.MethodGet, "/api/v1/assistant/conversations/"+strconv.FormatInt(id, 10), nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if view["kind"] != "project" || view["purpose"] != "смета и подрядчики" {
		t.Fatalf("conversation view = %v", view)
	}
}

func TestAssistantRoutesTopicIntoWorkspace(t *testing.T) {
	// The model creates a workspace and delegates the work into it by title,
	// mirroring how routing is supposed to happen from the main chat.
	provider := &httpScriptedProvider{responses: []assistant.Completion{
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-ws",
			Name:      "create_workspace",
			Arguments: json.RawMessage(`{"title":"Диссертация","purpose":"план глав"}`),
		}}},
		{ToolCalls: []assistant.ToolCall{{
			ID:        "call-delegate",
			Name:      "delegate_to_workspace",
			Arguments: json.RawMessage(`{"title":"Диссертация","task":"Составь план глав"}`),
		}}},
		{Content: "Пространство создано, работа передана."},
		// The delegated turn inside the workspace consumes the next response.
		{Content: "Пространство создано, работа передана."},
	}}
	handler, cleanup := newWorkspaceAPI(t, provider)
	defer cleanup()

	recorder, response := apiRequest(t, handler, http.MethodPost, "/api/v1/assistant/messages", map[string]any{
		"content": "Давай отдельно разберём диссертацию",
	}, nil)
	requireStatus(t, recorder, http.StatusOK)

	message := response["message"].(map[string]any)
	if message["content"] != "Пространство создано, работа передана." {
		t.Fatalf("message = %v", message["content"])
	}

	// The delegated turn ran inside the workspace conversation and produced its
	// own assistant answer there.
	recorder, view := apiRequest(t, handler, http.MethodGet, "/api/v1/assistant/conversations/2", nil, nil)
	requireStatus(t, recorder, http.StatusOK)
	if view["kind"] != "project" {
		t.Fatalf("workspace kind = %v", view["kind"])
	}
	answered := false
	for _, item := range view["messages"].([]any) {
		msg := item.(map[string]any)
		if msg["role"] == "assistant" && msg["content"] == "Пространство создано, работа передана." {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("delegated turn produced no answer: %v", view["messages"])
	}
}
