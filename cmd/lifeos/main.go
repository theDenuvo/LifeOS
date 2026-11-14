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
	"github.com/theDenuvo/LifeOS/internal/agent/tools"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	appcalendar "github.com/theDenuvo/LifeOS/internal/application/calendar"
	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/profile"
	"github.com/theDenuvo/LifeOS/internal/application/search"
	"github.com/theDenuvo/LifeOS/internal/application/settings"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
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
	taskService := tasks.NewService(
		sqlite.NewTaskUnitOfWork(db),
		clock,
		tasks.WithDefaultTimezone(cfg.Timezone),
		tasks.WithPublisher(broker),
	)
	inboxService := inbox.NewService(sqlite.NewInboxUnitOfWork(db), clock, cfg.Timezone, inbox.WithPublisher(broker))
	summaryService := summary.NewService(taskService, inboxService, clock, cfg.Timezone)
	proactiveService := proactive.NewService(
		sqlite.NewInsightUnitOfWork(db),
		taskService,
		inboxService,
		clock,
		cfg.Timezone,
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
	registry := tools.NewLifeOSTools(
		taskService,
		inboxService,
		summaryService,
		tools.WithSearchService(searchService),
		tools.WithProactiveService(proactiveService),
		tools.WithEventService(eventService),
		tools.WithDocumentService(documentService),
		tools.WithMachineAccess(cfg.ToolsRoot, cfg.ToolsShell, cfg.DatabasePath()),
		tools.WithTimezone(cfg.Timezone),
	)
	contextProvider := assistant.NewLifeOSContext(summaryService, proactiveService, cfg.Timezone, clock, assistant.WithEvents(eventService))
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
		assistant.WithWorkingDirectory(workingDirectory()),
	)

	// Create HTTP handler with all routes
	handler := httptransport.NewHandler(db, cfg, dist.FS, httptransport.Services{
		Tasks:     taskService,
		Inbox:     inboxService,
		Summary:   summaryService,
		Assistant: assistantService,
		Proactive: proactiveService,
		Search:    searchService,
		Settings:  settingsService,
		Profile:   profileService,
		Events:    eventService,
		Calendar:  calendarService,
		Documents: documentService,
		Models:    modelCatalog,
		Tools:     registry,
		Broker:    broker,
		Version:   version,
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
func workingDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

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
