package httptransport

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/config"
)

type Handler struct {
	db     *sql.DB
	cfg    *config.Config
	embedFS embed.FS
}

func NewHandler(db *sql.DB, cfg *config.Config, embedFS embed.FS) http.Handler {
	h := &Handler{db: db, cfg: cfg, embedFS: embedFS}
	return h.routes()
}

func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/healthz", h.healthz)

	// API v1
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", h.apiRoutes()))

	// MCP (if enabled)
	if h.cfg.MCPEnabled {
		mux.Handle(h.cfg.MCPPath+"/", http.StripPrefix(h.cfg.MCPPath, h.mcpRoutes()))
	}

	// Frontend (catch-all)
	mux.HandleFunc("/", h.serveFrontend)

	// Middleware chain
	return h.loggingMiddleware(h.corsMiddleware(mux))
}

func (h *Handler) apiRoutes() http.Handler {
	mux := http.NewServeMux()

	// Tasks
	mux.HandleFunc("GET /tasks", h.listTasks)
	mux.HandleFunc("POST /tasks", h.createTask)
	mux.HandleFunc("GET /tasks/{id}", h.getTask)
	mux.HandleFunc("PATCH /tasks/{id}", h.updateTask)
	mux.HandleFunc("POST /tasks/{id}/complete", h.completeTask)
	mux.HandleFunc("POST /tasks/{id}/cancel", h.cancelTask)
	mux.HandleFunc("POST /tasks/{id}/restore", h.restoreTask)
	mux.HandleFunc("POST /tasks/{id}/reschedule", h.rescheduleTask)

	// Inbox
	mux.HandleFunc("GET /inbox", h.listInbox)
	mux.HandleFunc("POST /inbox", h.captureInbox)
	mux.HandleFunc("POST /inbox/{id}/convert", h.convertInbox)
	mux.HandleFunc("DELETE /inbox/{id}", h.dismissInbox)

	// Summary
	mux.HandleFunc("GET /summary/today", h.todaySummary)

	// Assistant
	mux.HandleFunc("POST /assistant/messages", h.assistantMessage)
	mux.HandleFunc("GET /assistant/conversations", h.listConversations)
	mux.HandleFunc("GET /assistant/conversations/{id}", h.getConversation)
	mux.HandleFunc("POST /assistant/pending-actions/{id}/confirm", h.confirmPendingAction)
	mux.HandleFunc("POST /assistant/pending-actions/{id}/reject", h.rejectPendingAction)

	// Events (SSE)
	mux.HandleFunc("GET /events", h.events)

	// Settings
	mux.HandleFunc("GET /settings/{key}", h.getSetting)
	mux.HandleFunc("PUT /settings/{key}", h.putSetting)

	return mux
}

func (h *Handler) mcpRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.mcpHandler)
	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "dev",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) serveFrontend(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" || path == "" {
		path = "/index.html"
	}

	// Embedded files are under "build/" prefix
	embedPath := "build" + path

	// Try embedded files first
	f, err := h.embedFS.Open(strings.TrimPrefix(embedPath, "/"))
	if err == nil {
		defer f.Close()
		stat, _ := f.Stat()
		if !stat.IsDir() {
			http.ServeContent(w, r, path, stat.ModTime(), f.(io.ReadSeeker))
			return
		}
	}

	// Debug: list what's in embedFS
	entries, _ := h.embedFS.ReadDir("build")
	for _, e := range entries {
		slog.Debug("embedFS build entry", "name", e.Name(), "isDir", e.IsDir())
	}

	// SPA fallback - serve index.html
	f, err = h.embedFS.Open("build/index.html")
	if err != nil {
		slog.Error("Failed to open build/index.html", "error", err)
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Frontend not found", "Frontend not built or embedded")
		return
	}
	defer f.Close()
	stat, _ := f.Stat()
	http.ServeContent(w, r, "index.html", stat.ModTime(), f.(io.ReadSeeker))
}

func (h *Handler) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Debug("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start).String(),
			"remote", r.RemoteAddr,
		)
	})
}

func (h *Handler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, If-Match")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			slog.Error("JSON encode error", "error", err)
		}
	}
}

func (h *Handler) writeProblem(w http.ResponseWriter, r *http.Request, status int, ptype, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type":     "https://lifeos.dev/errors/" + ptype,
		"title":    title,
		"status":   status,
		"detail":   detail,
		"instance": r.URL.Path,
	})
}

// --- Request/Response types ---

type TaskResponse struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Note          *string    `json:"note,omitempty"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	PlannedDate   *string    `json:"planned_date,omitempty"`
	DueDate       *string    `json:"due_date,omitempty"`
	DueAt         *string    `json:"due_at,omitempty"`
	Timezone      string     `json:"timezone"`
	Tags          []string   `json:"tags"`
	CreatedAt     string     `json:"created_at"`
	UpdatedAt     string     `json:"updated_at"`
	CompletedAt   *string    `json:"completed_at,omitempty"`
	CancelledAt   *string    `json:"cancelled_at,omitempty"`
	Overdue       bool       `json:"overdue"`
}

type InboxItemResponse struct {
	ID         int64  `json:"id"`
	Text       string `json:"text"`
	Meta       string `json:"meta,omitempty"`
	CreatedAt  string `json:"created_at"`
	ProcessedAt *string `json:"processed_at,omitempty"`
}

type SummaryResponse struct {
	Today          []TaskSummaryItem `json:"today"`
	Overdue        []TaskSummaryItem `json:"overdue"`
	HighPriority   []TaskSummaryItem `json:"high_priority"`
	Stats          SummaryStats      `json:"stats"`
	FocusTask      *TaskSummaryItem  `json:"focus_task,omitempty"`
}

type TaskSummaryItem struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Time     string `json:"time"`
	Priority string `json:"priority"`
}

type SummaryStats struct {
	ActiveTotal       int `json:"active_total"`
	OverdueCount      int `json:"overdue_count"`
	HighPriorityCount int `json:"high_priority_count"`
	CompletedToday    int `json:"completed_today"`
	InboxCount        int `json:"inbox_count"`
}

type AssistantMessageRequest struct {
	Content         string `json:"content"`
	ConversationID  *int64 `json:"conversation_id,omitempty"`
}

type AssistantMessageResponse struct {
	MessageID      int64  `json:"message_id"`
	ConversationID int64  `json:"conversation_id"`
	Status         string `json:"status"` // "processing"
}

type ErrorResponse struct {
	Errors []FieldError `json:"errors"`
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PendingActionResponse struct {
	ID              int64  `json:"id"`
	ConversationID  int64  `json:"conversation_id"`
	MessageID       int64  `json:"message_id"`
	ToolName        string `json:"tool_name"`
	Arguments       string `json:"arguments"`
	Preview         string `json:"preview"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	ResolvedAt      *string `json:"resolved_at,omitempty"`
	Resolution      *string `json:"resolution,omitempty"`
}

// --- Handlers (stubs for Phase 1) ---

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": []TaskResponse{},
		"meta": map[string]int{"limit": 50, "offset": 0, "total": 0},
	})
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusCreated, TaskResponse{
		ID:        1,
		Title:     "Demo Task",
		Priority:  "medium",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:        1,
		Title:     "Demo Task",
		Priority:  "medium",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:        1,
		Title:     "Updated Task",
		Priority:  "high",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:          1,
		Title:       "Demo Task",
		Priority:    "medium",
		Status:      "completed",
		Timezone:    h.cfg.Timezone,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		CompletedAt: ptr(time.Now().UTC().Format(time.RFC3339)),
		Overdue:     false,
	})
}

func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:           1,
		Title:        "Demo Task",
		Priority:     "medium",
		Status:       "cancelled",
		Timezone:     h.cfg.Timezone,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		CancelledAt:  ptr(time.Now().UTC().Format(time.RFC3339)),
		Overdue:      false,
	})
}

func (h *Handler) restoreTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:        1,
		Title:     "Demo Task",
		Priority:  "medium",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) rescheduleTask(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, TaskResponse{
		ID:        1,
		Title:     "Demo Task",
		Priority:  "medium",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) listInbox(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": []InboxItemResponse{},
		"meta": map[string]int{"limit": 50, "offset": 0, "total": 0},
	})
}

func (h *Handler) captureInbox(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusCreated, InboxItemResponse{
		ID:        1,
		Text:      "Demo inbox item",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) convertInbox(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusCreated, TaskResponse{
		ID:        1,
		Title:     "Converted from inbox",
		Priority:  "medium",
		Status:    "open",
		Timezone:  h.cfg.Timezone,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Overdue:   false,
	})
}

func (h *Handler) dismissInbox(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) todaySummary(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, SummaryResponse{
		Today:        []TaskSummaryItem{},
		Overdue:      []TaskSummaryItem{},
		HighPriority: []TaskSummaryItem{},
		Stats: SummaryStats{
			ActiveTotal:       0,
			OverdueCount:      0,
			HighPriorityCount: 0,
			CompletedToday:    0,
			InboxCount:        0,
		},
	})
}

func (h *Handler) assistantMessage(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusAccepted, AssistantMessageResponse{
		MessageID:      1,
		ConversationID: 1,
		Status:         "processing",
	})
}

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
}

func (h *Handler) getConversation(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"id":      1,
		"title":   "Demo Conversation",
		"messages": []any{},
	})
}

func (h *Handler) confirmPendingAction(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, PendingActionResponse{
		ID:             1,
		ConversationID: 1,
		MessageID:      1,
		ToolName:       "create_task",
		Status:         "confirmed",
		ResolvedAt:     ptr(time.Now().UTC().Format(time.RFC3339)),
		Resolution:     ptr("confirmed"),
	})
}

func (h *Handler) rejectPendingAction(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, PendingActionResponse{
		ID:             1,
		ConversationID: 1,
		MessageID:      1,
		ToolName:       "create_task",
		Status:         "rejected",
		ResolvedAt:     ptr(time.Now().UTC().Format(time.RFC3339)),
		Resolution:     ptr("rejected"),
	})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("SSE: ResponseWriter does not support flushing")
		return
	}

	// Send initial event
	fmt.Fprintf(w, "event: connected\ndata: {\"message\":\"connected\"}\n\n")
	flusher.Flush()

	// Keep connection alive with periodic ping
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func (h *Handler) getSetting(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{"key": "demo", "value": map[string]string{}})
}

func (h *Handler) putSetting(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{"key": "demo", "value": map[string]string{}})
}

func (h *Handler) mcpHandler(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"result":  map[string]string{"status": "MCP endpoint - not implemented in Phase 1"},
		"id":      "1",
	})
}

func ptr[T any](v T) *T { return &v }