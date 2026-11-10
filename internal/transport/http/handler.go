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
	db      *sql.DB
	cfg     *config.Config
	embedFS embed.FS
}

func NewHandler(db *sql.DB, cfg *config.Config, embedFS embed.FS) http.Handler {
	h := &Handler{db: db, cfg: cfg, embedFS: embedFS}
	entries, _ := embedFS.ReadDir("build")
	fmt.Printf("[DEBUG] embedFS build entries: ")
	for _, e := range entries {
		fmt.Printf("%s(%v) ", e.Name(), e.IsDir())
	}
	fmt.Println()
	return h.routes()
}

func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.healthz)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", h.apiRoutes()))
	if h.cfg.MCPEnabled {
		mux.Handle(h.cfg.MCPPath+"/", http.StripPrefix(h.cfg.MCPPath, h.mcpRoutes()))
	}
	mux.HandleFunc("/", h.serveFrontend)
	return h.loggingMiddleware(h.corsMiddleware(mux))
}

func (h *Handler) apiRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks", h.notImplemented)
	mux.HandleFunc("POST /tasks", h.notImplemented)
	mux.HandleFunc("GET /tasks/{id}", h.notImplemented)
	mux.HandleFunc("PATCH /tasks/{id}", h.notImplemented)
	mux.HandleFunc("POST /tasks/{id}/complete", h.notImplemented)
	mux.HandleFunc("POST /tasks/{id}/cancel", h.notImplemented)
	mux.HandleFunc("POST /tasks/{id}/restore", h.notImplemented)
	mux.HandleFunc("POST /tasks/{id}/reschedule", h.notImplemented)
	mux.HandleFunc("GET /inbox", h.notImplemented)
	mux.HandleFunc("POST /inbox", h.notImplemented)
	mux.HandleFunc("POST /inbox/{id}/convert", h.notImplemented)
	mux.HandleFunc("DELETE /inbox/{id}", h.notImplemented)
	mux.HandleFunc("GET /summary/today", h.notImplemented)
	mux.HandleFunc("POST /assistant/messages", h.notImplemented)
	mux.HandleFunc("GET /assistant/conversations", h.notImplemented)
	mux.HandleFunc("GET /assistant/conversations/{id}", h.notImplemented)
	mux.HandleFunc("POST /assistant/pending-actions/{id}/confirm", h.notImplemented)
	mux.HandleFunc("POST /assistant/pending-actions/{id}/reject", h.notImplemented)
	mux.HandleFunc("GET /events", h.notImplemented)
	mux.HandleFunc("GET /settings/{key}", h.notImplemented)
	mux.HandleFunc("PUT /settings/{key}", h.notImplemented)
	return mux
}

func (h *Handler) mcpRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.notImplemented)
	return mux
}

func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	h.writeProblem(w, r, http.StatusNotImplemented, "not-implemented", "Not Implemented", "This endpoint is not yet implemented")
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "version": "dev", "time": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) serveFrontend(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" || path == "" {
		path = "/index.html"
	}
	embedPath := "build" + path
	fmt.Printf("[DEBUG] serveFrontend: path=%s, embedPath=%s\n", path, embedPath)
	f, err := h.embedFS.Open(strings.TrimPrefix(embedPath, "/"))
	if err == nil {
		defer f.Close()
		stat, _ := f.Stat()
		fmt.Printf("[DEBUG] First try: err=%v, IsDir=%v, size=%d\n", err, stat.IsDir(), stat.Size())
		if !stat.IsDir() {
			buf := make([]byte, 100)
			n, _ := f.Read(buf)
			fmt.Printf("[DEBUG] First 100 bytes: %s\n", string(buf[:n]))
			seeker, ok := f.(io.ReadSeeker)
			if ok {
				seeker.Seek(0, 0)
			}
			slog.Debug("Serving embedded file", "path", path, "embedPath", embedPath, "size", stat.Size())
			http.ServeContent(w, r, path, stat.ModTime(), seeker)
			return
		}
	} else {
		fmt.Printf("[DEBUG] First try error: %v\n", err)
	}
	f, err = h.embedFS.Open("build/index.html")
	if err != nil {
		slog.Error("Failed to open build/index.html", "error", err)
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Frontend not found", "Frontend not built or embedded")
		return
	}
	defer f.Close()
	stat, _ := f.Stat()
	buf := make([]byte, 100)
	n, _ := f.Read(buf)
	fmt.Printf("[DEBUG] SPA fallback first 100 bytes: %s\n", string(buf[:n]))
	seeker, ok := f.(io.ReadSeeker)
	if ok {
		seeker.Seek(0, 0)
	}
	fmt.Printf("[DEBUG] SPA fallback: size=%d\n", stat.Size())
	slog.Debug("Serving SPA fallback", "size", stat.Size())
	http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
}

func (h *Handler) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Debug("HTTP request", "method", r.Method, "path", r.URL.Path, "status", wrapped.statusCode, "duration", time.Since(start).String(), "remote", r.RemoteAddr)
	})
}

func (h *Handler) corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := map[string]bool{
		"http://localhost:5173": true, "http://127.0.0.1:5173": true,
		"http://localhost:8787": true, "http://127.0.0.1:8787": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
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

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
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
		"type": "https://lifeos.dev/errors/" + ptype, "title": title, "status": status, "detail": detail, "instance": r.URL.Path,
	})
}
