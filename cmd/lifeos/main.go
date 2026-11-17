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
	"github.com/theDenuvo/LifeOS/internal/application/automation"
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
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
	"github.com/theDenuvo/LifeOS/internal/events"
	infradocuments "github.com/theDenuvo/LifeOS/internal/infrastructure/documents"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
	"github.com/theDenuvo/LifeOS/internal/transport/http"
	"github.com/theDenuvo/LifeOS/internal/web/dist"
)

var version = "dev"

// eventIDBlock is how many event ids one process reserves at startup. It trades
// a bounded gap in the ids after a restart for a single write instead of a write
// per published event.
const eventIDBlock = 4096

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
	// Event ids are durable, not per-process. A browser that reconnects after a
	// restart sends the last id it saw, so a counter starting again at one would
	// make every new event look older than the client's position and be dropped.
	// The broker keeps its ids inside the durably reserved range and renews the
	// range before it runs out, and each range carries an epoch: a browser that
	// answers with ids from a restored older database is told to resynchronise.
	//
	// A failed reservation means the stream could not prove id uniqueness, so
	// the application must not start on it.
	idSequence := sqlite.NewEventSequence(db)
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, eventIDBlock, idSequence.Reserve)
	if err != nil {
		slog.Error("Could not reserve durable event ids; refusing to run the stream on a reusable counter", "error", err)
		os.Exit(1)
	}
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

	// Roadmap stage 5: full proactivity. The detectors now cross-reference
	// tasks with the calendar, goals with their deadlines and durable
	// agreements with their own age, and the outreach engine decides when one
	// of the resulting insights is worth starting a dialogue about.
	proactiveService := proactive.NewService(
		sqlite.NewInsightUnitOfWork(db),
		taskService,
		inboxService,
		clock,
		cfg.Timezone,
		proactive.WithGoals(goalsService),
		proactive.WithDeadlineRisks(goalsService),
		proactive.WithCalendar(eventService),
		proactive.WithMemory(memoryAgreements{service: memoryService}),
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

	// Roadmap stage 6: user automations. The registry doubles as the step
	// executor, so it is built first; the automation tools attach right after
	// the service exists. Step validation refuses tools an unattended
	// schedule may not run (infrastructure changes, workspace routing and the
	// automation tools themselves).
	automationService := automation.NewService(
		sqlite.NewAutomationUnitOfWork(db),
		clock,
		automation.WithPublisher(broker),
		automation.WithTools(registry),
		automation.WithValidator(tools.AutomationStepValidator(registry)),
		automation.WithConditions(automationInsights{service: proactiveService}),
		automation.WithTimezone(cfg.Timezone),
	)
	tools.RegisterAutomationTools(registry, automationService)

	// Roadmap stage 5: the initiative engine. It writes into the main chat on
	// LifeOS's own behalf, so it is built after the assistant storage exists.
	outreachConfig := proactive.OutreachConfig{
		QuietFrom:    cfg.OutreachQuietFrom,
		QuietTo:      cfg.OutreachQuietTo,
		MinInterval:  time.Duration(cfg.OutreachMinIntervalMinutes) * time.Minute,
		KindCooldown: time.Duration(cfg.OutreachKindCooldownHours) * time.Hour,
	}
	outreachService := proactive.NewOutreachService(
		sqlite.NewInsightUnitOfWork(db),
		sqlite.NewOutreachAssistant(db),
		broker,
		clock,
		cfg.Timezone,
		proactive.WithOutreachSettings(settingsService),
		proactive.WithOutreachCalendar(eventService),
		proactive.WithOutreachConfig(outreachConfig),
	)
	if !cfg.OutreachEnabled {
		// The env switch is the hard off; the runtime setting stays available
		// for the soft off the user controls from the interface.
		outreachService.Disable()
	}

	// Create HTTP handler with all routes
	handler := httptransport.NewHandler(db, cfg, dist.FS, httptransport.Services{
		Tasks:       taskService,
		Inbox:       inboxService,
		Summary:     summaryService,
		Assistant:   assistantService,
		Proactive:   proactiveService,
		Outreach:    outreachService,
		Search:      searchService,
		Settings:    settingsService,
		Profile:     profileService,
		Events:      eventService,
		Calendar:    calendarService,
		Documents:   documentService,
		Planning:    planningService,
		Recurrence:  recurrenceService,
		Completion:  completionService,
		Memory:      memoryService,
		Goals:       goalsService,
		Automations: automationService,
		Models:      modelCatalog,
		Tools:       registry,
		Broker:      broker,
		Version:     version,
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

	// Roadmap stage 6: release the automation reservations a previous process
	// left running BEFORE anything can start a new run. LifeOS is
	// single-process, so every running run is stale at startup. This must
	// happen before the HTTP server opens: a recovery that races a legitimate
	// run would mark that run failed while its steps keep executing. A failed
	// recovery leaves the reservation state unproven, so the application
	// refuses to run rather than block or double-run automations.
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := automationService.RecoverStaleRuns(recoveryCtx); err != nil {
		recoveryCancel()
		slog.Error("Could not recover automation runs from an earlier process; refusing to start on an unproven reservation state", "error", err)
		os.Exit(1)
	}
	recoveryCancel()

	go func() {
		slog.Info("HTTP server listening", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
			stop()
		}
	}()

	// Proactive runner: recompute insights periodically, announce new ones and
	// run the initiative gate over the fresh picture.
	go proactive.NewRunner(proactiveService, broker, 15*time.Minute, outreachService).Start(ctx)

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

	// Automation runner: schedule moments fire on a fixed cadence, LifeOS
	// events are observed live through the broker, and condition triggers are
	// checked on every pass. Both loops stop with the context, and Start
	// returns only after the refusal journal has drained, so the wait below
	// must complete before the database closes.
	automationRunnerDone := make(chan struct{})
	go func() {
		automation.NewRunner(automationService, broker, 30*time.Second).Start(ctx)
		close(automationRunnerDone)
	}()

	<-ctx.Done()
	slog.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
	} else {
		slog.Info("Server stopped gracefully")
	}

	// The automation runner finishes its in-flight runs and flushes the
	// refusal journal on shutdown; wait for it before returning — and before
	// the deferred database close — so no write is cut off. The wait covers
	// the runner's own worst case: a 10-second bound on in-flight runs plus a
	// 10-second bound on the journal drain, plus slack. A runner that still
	// has not finished is reported instead of holding the process open
	// indefinitely; everything it is doing is deadline-bound and fails on its
	// own shortly after.
	select {
	case <-automationRunnerDone:
	case <-time.After(25 * time.Second):
		slog.Warn("Automation runner did not finish in time; continuing shutdown")
	}
}

// automationInsights feeds condition triggers from the proactive service:
// the keys of the insights visible right now.
type automationInsights struct {
	service *proactive.Service
}

func (a automationInsights) CurrentInsightKeys(ctx context.Context) (map[string]struct{}, error) {
	insights, err := a.service.Compute(ctx)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(insights))
	for _, insight := range insights {
		keys[insight.Key] = struct{}{}
	}
	return keys, nil
}

// workingDirectory is the base for relative paths in the file tools.
func workingDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// memoryAgreements feeds the forgotten-commitment detector from the durable
// memory: active agreements are exactly the promises worth revisiting.
type memoryAgreements struct {
	service *appmemory.Service
}

func (a memoryAgreements) Agreements(ctx context.Context) ([]proactive.Agreement, error) {
	category := domainmemory.CategoryAgreement
	status := domainmemory.StatusActive
	entries, _, err := a.service.List(ctx, appmemory.Filter{
		Category: &category,
		Status:   &status,
		Limit:    200,
	})
	if err != nil {
		return nil, err
	}
	result := make([]proactive.Agreement, 0, len(entries))
	for _, entry := range entries {
		result = append(result, proactive.Agreement{
			ID:        entry.ID,
			Content:   entry.Content,
			UpdatedAt: entry.UpdatedAt,
		})
	}
	return result, nil
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
