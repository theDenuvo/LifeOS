package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:8787" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:8787")
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.Timezone != "Europe/Moscow" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "Europe/Moscow")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.LLMProvider != "openrouter" {
		t.Errorf("LLMProvider = %q, want %q", cfg.LLMProvider, "openrouter")
	}
	if cfg.LLMBaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("LLMBaseURL = %q, want %q", cfg.LLMBaseURL, "https://openrouter.ai/api/v1")
	}
	if cfg.LLMAPIKey != "" {
		t.Errorf("LLMAPIKey = %q, want empty", cfg.LLMAPIKey)
	}
	if cfg.LLMModel != "openai/gpt-4o-mini" {
		t.Errorf("LLMModel = %q, want %q", cfg.LLMModel, "openai/gpt-4o-mini")
	}
	if cfg.MCPEnabled != false {
		t.Errorf("MCPEnabled = %v, want false", cfg.MCPEnabled)
	}
	if cfg.MCPPath != "/mcp" {
		t.Errorf("MCPPath = %q, want %q", cfg.MCPPath, "/mcp")
	}
}

func TestLoadValidTimezone(t *testing.T) {
	os.Setenv("LIFEOS_TIMEZONE", "Europe/Moscow")
	defer os.Unsetenv("LIFEOS_TIMEZONE")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Timezone != "Europe/Moscow" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "Europe/Moscow")
	}
}

func TestLoadInvalidTimezone(t *testing.T) {
	os.Setenv("LIFEOS_TIMEZONE", "Invalid/Timezone")
	defer os.Unsetenv("LIFEOS_TIMEZONE")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load() should fail with invalid timezone")
	}
}

func TestLoadEnvOverride(t *testing.T) {
	os.Setenv("LIFEOS_LISTEN_ADDR", "0.0.0.0:9999")
	os.Setenv("LIFEOS_TIMEZONE", "UTC")
	os.Setenv("LIFEOS_LOG_LEVEL", "debug")
	os.Setenv("LIFEOS_MCP_ENABLED", "true")
	defer func() {
		os.Unsetenv("LIFEOS_LISTEN_ADDR")
		os.Unsetenv("LIFEOS_TIMEZONE")
		os.Unsetenv("LIFEOS_LOG_LEVEL")
		os.Unsetenv("LIFEOS_MCP_ENABLED")
	}()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ListenAddr != "0.0.0.0:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "0.0.0.0:9999")
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "UTC")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.MCPEnabled != true {
		t.Errorf("MCPEnabled = %v, want true", cfg.MCPEnabled)
	}
}

func TestLoadDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")

	content := `LIFEOS_LISTEN_ADDR=0.0.0.0:8888
LIFEOS_TIMEZONE=America/New_York
LIFEOS_LOG_LEVEL=warn
`
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := Load(envFile)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ListenAddr != "0.0.0.0:8888" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "0.0.0.0:8888")
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "America/New_York")
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "warn")
	}
}

func TestLoadDotEnvEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")

	content := `LIFEOS_LISTEN_ADDR=0.0.0.0:8888
LIFEOS_TIMEZONE=America/New_York
`
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Env var should override .env
	os.Setenv("LIFEOS_LISTEN_ADDR", "127.0.0.1:7777")
	defer os.Unsetenv("LIFEOS_LISTEN_ADDR")

	cfg, err := Load(envFile)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:7777" {
		t.Errorf("ListenAddr = %q, want %q (env should override .env)", cfg.ListenAddr, "127.0.0.1:7777")
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "America/New_York")
	}
}

func TestLoadDotEnvMissing(t *testing.T) {
	// Non-existent .env should not error
	cfg, err := Load("/nonexistent/path/.env")
	if err != nil {
		t.Fatalf("Load() should not fail for missing .env: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8787" {
		t.Errorf("ListenAddr should use default")
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		defVal   bool
		expected bool
	}{
		{"true string", "true", false, true},
		{"false string", "false", true, false},
		{"1 string", "1", false, true},
		{"0 string", "0", true, false},
		{"empty string", "", true, true},
		{"invalid string", "invalid", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVal != "" {
				os.Setenv("TEST_BOOL", tc.envVal)
				defer os.Unsetenv("TEST_BOOL")
			}
			result := getEnvBool("TEST_BOOL", tc.defVal)
			if result != tc.expected {
				t.Errorf("getEnvBool(%q, %v) = %v, want %v", tc.envVal, tc.defVal, result, tc.expected)
			}
		})
	}
}

func TestDatabasePath(t *testing.T) {
	cfg := &Config{DataDir: "/custom/data"}
	path := cfg.DatabasePath()
	expected := filepath.Join("/custom/data", "lifeos.db")
	if path != expected {
		t.Errorf("DatabasePath() = %q, want %q", path, expected)
	}
}
