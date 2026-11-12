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
	"syscall"
	"time"

	"github.com/theDenuvo/LifeOS/internal/agent"
	"github.com/theDenuvo/LifeOS/internal/agent/tools"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/events"
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

	// Assistant runtime: an OpenAI-compatible provider plus the LifeOS tools.
	var provider assistant.Provider
	if cfg.LLMAPIKey != "" || cfg.LLMProvider == "ollama" {
		provider = agent.NewOpenAIProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	}
	registry := tools.NewLifeOSTools(taskService, inboxService, summaryService)
	assistantService := assistant.NewService(
		sqlite.NewAssistantUnitOfWork(db),
		provider,
		registry,
		clock,
		assistant.WithTimezone(cfg.Timezone),
		assistant.WithPublisher(broker),
	)

	proactiveService := proactive.NewService(
		sqlite.NewInsightUnitOfWork(db),
		taskService,
		inboxService,
		clock,
		cfg.Timezone,
	)

	// Create HTTP handler with all routes
	handler := httptransport.NewHandler(db, cfg, dist.FS, httptransport.Services{
		Tasks:     taskService,
		Inbox:     inboxService,
		Summary:   summaryService,
		Assistant: assistantService,
		Proactive: proactiveService,
		Tools:     registry,
		Events:    broker,
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
