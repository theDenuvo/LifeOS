package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/theDenuvo/LifeOS/internal/agent"
	agentdocuments "github.com/theDenuvo/LifeOS/internal/agent/documents"
	agentmemory "github.com/theDenuvo/LifeOS/internal/agent/memory"
	"github.com/theDenuvo/LifeOS/internal/agent/tools"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	appcalendar "github.com/theDenuvo/LifeOS/internal/application/calendar"
	"github.com/theDenuvo/LifeOS/internal/application/completion"
	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/goals"
	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	"github.com/theDenuvo/LifeOS/internal/application/planning"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/profile"
	"github.com/theDenuvo/LifeOS/internal/application/recurrence"
	"github.com/theDenuvo/LifeOS/internal/application/search"
	"github.com/theDenuvo/LifeOS/internal/application/settings"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/events"
	infradocuments "github.com/theDenuvo/LifeOS/internal/infrastructure/documents"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
	"github.com/theDenuvo/LifeOS/internal/transport/http"
	"github.com/theDenuvo/LifeOS/internal/web/dist"
)

var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "Show version and exit")
		configPath  = flag.String("config", "", "Path to config file (optional)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("LifeOS %s\n", version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Setup structured logging
	setupLogging(cfg.LogLevel)

	slog.Info("Starting LifeOS", "version", version, "listen", cfg.ListenAddr)

	// Initialize SQLite
	db, err := sqlite.Open(cfg.DatabasePath())
	if err != nil {
		slog.Error("Failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	if err := sqlite.Migrate(db); err != nil {
		slog.Error("Failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Application services over the SQLite units of work.
	clock := tasks.SystemClock()
	broker := events.NewBroker()
	// planningService is assigned below; the task engine keeps the reference so a
	// task change re-derives its reminders in the same breath.
	var planningService *planning.Service
	taskService := tasks.NewService(
		sqlite.NewTaskUnitOfWork(db),
		clock,
		tasks.WithDefaultTimezone(cfg.Timezone),
		tasks.WithPublisher(broker),
		tasks.WithReminderSink(tasks.ReminderFunc(func(ctx context.Context, value task.Task) error {
			if planningService == nil {
				return nil
			}
			return planningService.SyncTaskReminders(ctx, value)
		})),
	)
	inboxService := inbox.NewService(sqlite.NewInboxUnitOfWork(db), clock, cfg.Timezone, inbox.WithPublisher(broker))
	summaryService := summary.NewService(taskService, inboxService, clock, cfg.Timezone)

	// Roadmap stage 4: long-term goals and initiative management.
	//
	// A drafted goal waits for user approval, approved plans change only
	// through proposals, and progress is derived from milestones and the tasks
	// actually linked to the goal. It is built before the proactive service so
	// the stalled-goal detector can join the insight stream.
	goalsService := goals.NewService(
		sqlite.NewGoalUnitOfWork(db),
		clock,
		goals.WithTaskProvider(goals.TaskEngineAdapter{Service: taskService}),
		goals.WithPublisher(broker),
	)
	proactiveService := proactive.NewService(
		sqlite.NewInsightUnitOfWork(db),
		taskService,
		inboxService,
		clock,
		cfg.Timezone,
		proactive.WithGoals(goalsService),
	)
	searchService := search.NewService(taskService, inboxService)
	eventService := appevents.NewService(
		sqlite.NewEventUnitOfWork(db),
		clock,
		appevents.WithTimezone(cfg.Timezone),
		appevents.WithPublisher(broker),
	)
	calendarService := appcalendar.NewService(eventService, taskService, cfg.Timezone)
	settingsService := settings.NewService(sqlite.NewSettingsUnitOfWork(db), clock)
	profileService := profile.NewService(settingsService)
	modelCatalog := agent.NewModelCatalog(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	documentStore, err := infradocuments.NewLocalStore(cfg.DocumentsDir)
	if err != nil {
		slog.Error("Failed to initialize document storage", "error", err)
		os.Exit(1)
	}
	documentService := appdocuments.NewService(
		sqlite.NewDocumentUnitOfWork(db),
		documentStore,
		infradocuments.NewExtractor(),
		agentdocuments.NewExtractor(cfg.DocLLMBaseURL, cfg.DocLLMAPIKey, cfg.DocLLMModel),
		taskService,
		clock,
		cfg.Timezone,
	)

	// Stage 1 of the roadmap: planned working time, reminders and recurring tasks.
	//
	// The planner owns the time arithmetic, the recurrence service creates the next
	// occurrence of a habit, and the task engine calls back into both whenever a
	// task changes so nothing drifts apart.
	planningService = planning.NewService(
		sqlite.NewPlanningUnitOfWork(db),
		clock,
		planning.WithTimezone(cfg.Timezone),
		planning.WithPublisher(broker),
	)
	// The recurrence queue is derived from the tasks themselves, so a completion
	// whose next occurrence failed to appear is found again after a restart and
	// retried instead of being lost.
	recurrenceService := recurrence.NewService(
		taskService,
		clock,
		recurrence.WithTimezone(cfg.Timezone),
		recurrence.WithStore(sqlite.NewRecurrenceStore(db)),
	)

	// Completion is checked against the sources LifeOS really owns: uploaded
	// documents, the user's files on this machine and the calendar. GitHub and
	// messengers have no integration, so they are reported as unavailable instead
	// of being silently treated as "nothing found".
	completionRoot := cfg.ToolsRoot
	if completionRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			completionRoot = home
		}
	}
	completionService := completion.NewService(
		taskService,
		[]completion.Source{
			&completion.DocumentSource{Docs: sqlite.NewDocumentSearcher(db)},
			&completion.FilesSource{Root: completionRoot},
			&completion.CalendarSource{Events: eventService},
			completion.NewGitHubSource(),
			completion.NewMessengerSource(),
		},
		completion.WithStore(sqlite.NewCompletionStore(db)),
		completion.WithSettings(settingsService),
		completion.WithClock(clock),
	)

	// Roadmap stage 2: durable memory, provenance and the living knowledge base.
	//
	// Learning is autonomous (facts are extracted from conversations and
	// indexed documents), but every stored claim keeps its source, an explicit
	// certainty level and a version. Contradictions become a review item instead
	// of silently overwriting what the user already established.
	memoryService := appmemory.NewService(
		sqlite.NewMemoryUnitOfWork(db),
		clock,
		appmemory.WithExtractor(agentmemory.NewExtractor(cfg.DocLLMBaseURL, cfg.DocLLMAPIKey, cfg.DocLLMModel)),
		appmemory.WithPublisher(broker),
	)

	// The dashboard shows the day's load and the ideas that are due to resurface.
	summaryService.WithRevisit(inboxService).WithPlanner(planningService)

	// Completing a recurring task creates its next occurrence. The service is
	// built recursively, so the hook is attached after both exist.
	taskService.SetRecurrenceHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		return recurrenceService.Next(ctx, completed)
	})

	// Assistant runtime: an OpenAI-compatible provider plus the LifeOS tools.
	//
	// The provider is wrapped in a retry/fallback chain: a rate-limited or
	// unreachable model retries with backoff and then hands the turn to another
	// model instead of failing the user's message.
	var provider assistant.Provider
	if cfg.LLMAPIKey != "" || cfg.LLMProvider == "ollama" {
		base := agent.NewOpenAIProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		fallbacks := func(ctx context.Context) []string {
			// Order matters: explicit list, then the configured default model, then
			// the catalog (largest context first, so the fallback still fits the
			// conversation, documents and tool schemas).
			models := make([]string, 0, len(cfg.LLMFallbackModels)+4)
			models = append(models, cfg.LLMFallbackModels...)
			models = append(models, cfg.LLMModel)
			catalog := modelCatalog.List(ctx)
			sort.SliceStable(catalog, func(i, j int) bool {
				return catalog[i].ContextLength > catalog[j].ContextLength
			})
			for _, info := range catalog {
				models = append(models, info.ID)
			}
			return models
		}
		policy := agent.DefaultRetryPolicy()
		policy.AttemptsPerModel = cfg.LLMRetryAttempts
		policy.BaseDelay = time.Duration(cfg.LLMRetryDelayMS) * time.Millisecond
		provider = agent.NewRetryProvider(func(model string) assistant.Provider {
			if model == "" {
				return base
			}
			return base.WithModel(model)
		}, fallbacks, policy)
	}
	// Roadmap stage 3: project workspaces. The hub is created before the
	// registry (tools reference it) and bound to the assistant runtime after,
	// which is what lets the main chat route a topic into its own conversation
	// and lets a workspace report results back without manual copying.
	workspaceHub := tools.NewWorkspaceHub(sqlite.NewAssistantUnitOfWork(db), broker)
	registry := tools.NewLifeOSTools(
		taskService,
		inboxService,
		summaryService,
		tools.WithSearchService(searchService),
		tools.WithProactiveService(proactiveService),
		tools.WithEventService(eventService),
		tools.WithDocumentService(documentService),
		tools.WithMachineAccess(cfg.ToolsRoot, cfg.ToolsShell, cfg.DatabasePath()),
		tools.WithPlanning(planningService),
		tools.WithRecurrence(recurrenceService),
		tools.WithCompletion(completionService),
		tools.WithMemoryService(memoryService),
		tools.WithGoalsService(goalsService),
		tools.WithWorkspaceHub(workspaceHub),
		tools.WithTimezone(cfg.Timezone),
	)
	contextProvider := assistant.NewLifeOSContext(
		summaryService,
		proactiveService,
		cfg.Timezone,
		clock,
		assistant.WithEvents(eventService),
		assistant.WithMemory(memoryService),
		assistant.WithGoals(goalsService),
	)
	assistantService := assistant.NewService(
		sqlite.NewAssistantUnitOfWork(db),
		provider,
		registry,
		clock,
		assistant.WithTimezone(cfg.Timezone),
		assistant.WithMaxRounds(cfg.AssistantMaxRounds),
		assistant.WithPublisher(broker),
		assistant.WithContextProvider(contextProvider),
		assistant.WithDocumentTextProvider(documentTextProvider{service: documentService}),
		assistant.WithMemoryLearner(memoryService),
		assistant.WithWorkingDirectory(workingDirectory()),
	)
	workspaceHub.Bind(assistantService)

	// Create HTTP handler with all routes
	handler := httptransport.NewHandler(db, cfg, dist.FS, httptransport.Services{
		Tasks:      taskService,
		Inbox:      inboxService,
		Summary:    summaryService,
		Assistant:  assistantService,
		Proactive:  proactiveService,
		Search:     searchService,
		Settings:   settingsService,
		Profile:    profileService,
		Events:     eventService,
		Calendar:   calendarService,
		Documents:  documentService,
		Planning:   planningService,
		Recurrence: recurrenceService,
		Completion: completionService,
		Memory:     memoryService,
		Goals:      goalsService,
		Models:     modelCatalog,
		Tools:      registry,
		Broker:     broker,
		Version:    version,
	}).Routes()

	// HTTP Server
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("HTTP server listening", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
			stop()
		}
	}()

	// Proactive runner: recompute insights periodically and announce new ones.
	go proactive.NewRunner(proactiveService, broker, 15*time.Minute).Start(ctx)

	// Reminder delivery: reminders are planned when a task changes and published
	// here when their moment comes, so the browser shows them without polling.
	go planning.NewDispatcher(planningService, broker, clock, 30*time.Second).Run(ctx)
	go recurrence.NewWorker(recurrenceService, time.Minute).Run(ctx)

	// Goal progress is recorded synchronously when a completion commits. It
	// does not depend on a live event subscriber, and the result is on disk
	// before the completing request returns. The reconciler is the second line:
	// it restores entries whose write failed, and it repairs journals after an
	// interrupted run.
	taskService.SetCompletionHook(func(ctx context.Context, completed tasks.TaskWithVersion) error {
		// The error is returned, not swallowed: the task service logs it, and a
		// failed write stays recoverable because the completion itself is
		// durable in the task history.
		return goalsService.OnTaskCompleted(ctx, completed.Task.ID)
	})
	go goals.NewReconciler(goalsService, goals.DefaultReconcileInterval).Run(ctx)

	<-ctx.Done()
	slog.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
	} else {
		slog.Info("Server stopped gracefully")
	}
}

// workingDirectory is the base for relative paths in the file tools.
func workingDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// documentTextProvider feeds extracted document Markdown into the assistant
// prompt, so an attached file reaches every model instead of only its ID.
type documentTextProvider struct {
	service *appdocuments.Service
}

func (p documentTextProvider) DocumentText(ctx context.Context, id int64) (assistant.DocumentText, error) {
	view, err := p.service.Get(ctx, id)
	if err != nil {
		return assistant.DocumentText{}, err
	}
	if view.Document.ExtractedText == nil {
		return assistant.DocumentText{}, errors.New("документ ещё не сконвертирован")
	}
	return assistant.DocumentText{
		ID:       view.Document.ID,
		Filename: view.Document.Filename,
		Text:     *view.Document.ExtractedText,
	}, nil
}

// workingDirectory is the base for relative paths in the file tools.

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Remove sensitive fields from logs
			if a.Key == "api_key" || a.Key == "password" || a.Key == "token" {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(logger)
}
