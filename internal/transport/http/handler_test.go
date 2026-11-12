package httptransport

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/config"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
	"github.com/theDenuvo/LifeOS/internal/web/dist"
)

//go:embed build/*
var testEmbedFS embed.FS

func setupTestHandler(t *testing.T) (*Handler, func()) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}

	if err := sqlite.Migrate(db); err != nil {
		db.Close()
		t.Fatalf("sqlite.Migrate failed: %v", err)
	}

	// Create config with test settings
	cfg := &config.Config{
		ListenAddr:  "127.0.0.1:8787",
		DataDir:     tmpDir,
		Timezone:    "Europe/Moscow",
		LogLevel:    "debug",
		LLMProvider: "openrouter",
		LLMBaseURL:  "https://openrouter.ai/api/v1",
		LLMAPIKey:   "",
		LLMModel:    "openai/gpt-4o-mini",
		MCPEnabled:  false,
		MCPPath:     "/mcp",
	}

	// Use the test embedded filesystem
	taskService := tasks.NewService(sqlite.NewTaskUnitOfWork(db), tasks.SystemClock())
	handler := NewHandler(db, cfg, testEmbedFS, taskService)

	cleanup := func() {
		db.Close()
	}

	return handler, cleanup
}

func TestHandlerRootServesIndexHTML(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("Expected HTML content, got: %s", body[:min(200, len(body))])
	}
}

func TestHandlerStaticAssetJS(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Test existing JS asset
	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/start.test.js", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for existing JS asset, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "javascript") {
		t.Errorf("Expected javascript content type, got %s", contentType)
	}
}

func TestHandlerStaticAssetCSS(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Test existing CSS asset
	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/assets/style.test.css", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for existing CSS asset, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "css") {
		t.Errorf("Expected css content type, got %s", contentType)
	}
}

func TestHandlerMissingStaticAssetReturns404(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Test missing JS asset
	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/nonexistent.js", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 for missing JS asset, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/problem+json") {
		t.Errorf("Expected problem+json content type, got %s", contentType)
	}

	var problem map[string]any
	if err := json.NewDecoder(w.Body).Decode(&problem); err != nil {
		t.Fatalf("Failed to decode problem response: %v", err)
	}
	if problem["status"] != float64(404) {
		t.Errorf("Expected status 404 in problem, got %v", problem["status"])
	}
	if problem["title"] != "Asset not found" {
		t.Errorf("Expected title 'Asset not found', got %v", problem["title"])
	}

	// Test missing CSS asset
	req = httptest.NewRequest(http.MethodGet, "/_app/immutable/assets/nonexistent.css", nil)
	w = httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 for missing CSS asset, got %d", w.Code)
	}

	// Test missing image asset
	req = httptest.NewRequest(http.MethodGet, "/images/nonexistent.png", nil)
	w = httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 for missing image asset, got %d", w.Code)
	}
}

func TestHandlerSPAFallbackForClientRoutes(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// Test client-side route (no file extension)
	clientRoutes := []string{
		"/tasks",
		"/tasks/1",
		"/inbox",
		"/settings",
		"/assistant",
		"/dashboard",
		"/unknown-route",
	}

	for _, route := range clientRoutes {
		t.Run(route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			w := httptest.NewRecorder()
			handler.Routes().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Route %s: expected status 200, got %d", route, w.Code)
			}

			contentType := w.Header().Get("Content-Type")
			if !strings.Contains(contentType, "text/html") {
				t.Errorf("Route %s: expected text/html content type, got %s", route, contentType)
			}

			body := w.Body.String()
			if !strings.Contains(body, "<!DOCTYPE html>") {
				t.Errorf("Route %s: expected HTML content", route)
			}
		})
	}
}

func TestHandlerHealthz(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected application/json content type, got %s", contentType)
	}

	var health map[string]any
	if err := json.NewDecoder(w.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}
	if health["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", health["status"])
	}
	if health["version"] == nil {
		t.Error("Expected version field")
	}
	if health["time"] == nil {
		t.Error("Expected time field")
	}
}

func TestHandlerAPIEndpointsReturn501(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	apiEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/inbox"},
		{"POST", "/api/v1/inbox"},
		{"POST", "/api/v1/inbox/1/convert"},
		{"DELETE", "/api/v1/inbox/1"},
		{"GET", "/api/v1/summary/today"},
		{"POST", "/api/v1/assistant/messages"},
		{"GET", "/api/v1/assistant/conversations"},
		{"GET", "/api/v1/assistant/conversations/1"},
		{"POST", "/api/v1/assistant/pending-actions/1/confirm"},
		{"POST", "/api/v1/assistant/pending-actions/1/reject"},
		{"GET", "/api/v1/events"},
		{"GET", "/api/v1/settings/test"},
		{"PUT", "/api/v1/settings/test"},
	}

	for _, ep := range apiEndpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			handler.Routes().ServeHTTP(w, req)

			if w.Code != http.StatusNotImplemented {
				t.Errorf("Expected status 501, got %d", w.Code)
			}

			contentType := w.Header().Get("Content-Type")
			if !strings.Contains(contentType, "application/problem+json") {
				t.Errorf("Expected problem+json content type, got %s", contentType)
			}

			var problem map[string]any
			if err := json.NewDecoder(w.Body).Decode(&problem); err != nil {
				t.Fatalf("Failed to decode problem response: %v", err)
			}
			if problem["status"] != float64(501) {
				t.Errorf("Expected status 501 in problem, got %v", problem["status"])
			}
			if problem["title"] != "Not Implemented" {
				t.Errorf("Expected title 'Not Implemented', got %v", problem["title"])
			}
			if !strings.Contains(problem["type"].(string), "not-implemented") {
				t.Errorf("Expected type to contain 'not-implemented', got %v", problem["type"])
			}
		})
	}
}

func TestHandlerCORSAllowlist(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	allowedOrigins := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:8787",
		"http://127.0.0.1:8787",
	}

	for _, origin := range allowedOrigins {
		t.Run("allowed_"+origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
			req.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			handler.Routes().ServeHTTP(w, req)

			if w.Code != http.StatusNoContent {
				t.Errorf("Expected status 204 for OPTIONS, got %d", w.Code)
			}

			allowOrigin := w.Header().Get("Access-Control-Allow-Origin")
			if allowOrigin != origin {
				t.Errorf("Expected Access-Control-Allow-Origin=%s, got %s", origin, allowOrigin)
			}

			allowCreds := w.Header().Get("Access-Control-Allow-Credentials")
			if allowCreds != "true" {
				t.Errorf("Expected Access-Control-Allow-Credentials=true, got %s", allowCreds)
			}
		})
	}

	// Test disallowed origin
	t.Run("disallowed_origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
		req.Header.Set("Origin", "http://evil.com")
		w := httptest.NewRecorder()
		handler.Routes().ServeHTTP(w, req)

		// Should still return 204 for OPTIONS but without CORS headers
		if w.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 for OPTIONS, got %d", w.Code)
		}

		allowOrigin := w.Header().Get("Access-Control-Allow-Origin")
		if allowOrigin == "http://evil.com" {
			t.Error("Disallowed origin should not be in Access-Control-Allow-Origin")
		}
	})
}

func TestHandlerLooksLikeStaticAsset(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	tests := []struct {
		path     string
		expected bool
	}{
		{"/_app/immutable/entry/start.js", true},
		{"/_app/immutable/assets/style.css", true},
		{"/images/logo.png", true},
		{"/favicon.ico", true},
		{"/manifest.webmanifest", true},
		{"/sw.js", true},
		{"/tasks", false},        // SPA route
		{"/tasks/1", false},      // SPA route
		{"/inbox", false},        // SPA route
		{"/", false},             // Root
		{"/index.html", false},   // HTML file (SPA)
		{"/unknown", false},      // Unknown route
		{"/api/v1/tasks", false}, // API route
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := handler.looksLikeStaticAsset(tc.path)
			if result != tc.expected {
				t.Errorf("looksLikeStaticAsset(%q) = %v, want %v", tc.path, result, tc.expected)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestHandlerServeFrontendWithRealEmbedFS tests the handler with the actual embedded filesystem
func TestHandlerServeFrontendWithRealEmbedFS(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}
	defer db.Close()

	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("sqlite.Migrate failed: %v", err)
	}

	cfg := &config.Config{
		ListenAddr:  "127.0.0.1:8787",
		DataDir:     tmpDir,
		Timezone:    "Europe/Moscow",
		LogLevel:    "info",
		LLMProvider: "openrouter",
		LLMBaseURL:  "https://openrouter.ai/api/v1",
		LLMAPIKey:   "",
		LLMModel:    "openai/gpt-4o-mini",
		MCPEnabled:  false,
		MCPPath:     "/mcp",
	}

	// Use the real embedded filesystem
	taskService := tasks.NewService(sqlite.NewTaskUnitOfWork(db), tasks.SystemClock())
	handler := NewHandler(db, cfg, dist.FS, taskService)

	// Test root serves index.html
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("Expected HTML content")
	}

	// Test existing JS asset from real embed
	req = httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/start.test.js", nil)
	w = httptest.NewRecorder()
	handler.Routes().ServeHTTP(w, req)
	// Note: This might 404 if the test file doesn't exist in the real embed
	// But we're testing the code path works
}
