package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata"
)

type Config struct {
	ListenAddr string
	DataDir    string
	Timezone   string
	LogLevel   string

	LLMProvider string
	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string

	MCPEnabled bool
	MCPPath    string
}

func Load(configPath string) (*Config, error) {
	// Default to .env if not specified
	if configPath == "" {
		configPath = ".env"
	}

	cfg := &Config{
		ListenAddr:  getEnv("LIFEOS_LISTEN_ADDR", "127.0.0.1:8787"),
		DataDir:     getEnv("LIFEOS_DATA_DIR", "./data"),
		Timezone:    getEnv("LIFEOS_TIMEZONE", "Europe/Moscow"),
		LogLevel:    getEnv("LIFEOS_LOG_LEVEL", "info"),
		LLMProvider: getEnv("LIFEOS_LLM_PROVIDER", "openrouter"),
		LLMBaseURL:  getEnv("LIFEOS_LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMAPIKey:   getEnv("LIFEOS_LLM_API_KEY", ""),
		LLMModel:    getEnv("LIFEOS_LLM_MODEL", "openai/gpt-4o-mini"),
		MCPEnabled:  getEnvBool("LIFEOS_MCP_ENABLED", false),
		MCPPath:     getEnv("LIFEOS_MCP_PATH", "/mcp"),
	}

	// Load .env file if it exists (non-fatal if missing)
	if err := loadDotEnv(configPath); err != nil {
		return nil, err
	}
	// Re-read env vars after loading .env (env vars take precedence)
	cfg.ListenAddr = getEnv("LIFEOS_LISTEN_ADDR", cfg.ListenAddr)
	cfg.DataDir = getEnv("LIFEOS_DATA_DIR", cfg.DataDir)
	cfg.Timezone = getEnv("LIFEOS_TIMEZONE", cfg.Timezone)
	cfg.LogLevel = getEnv("LIFEOS_LOG_LEVEL", cfg.LogLevel)
	cfg.LLMProvider = getEnv("LIFEOS_LLM_PROVIDER", cfg.LLMProvider)
	cfg.LLMBaseURL = getEnv("LIFEOS_LLM_BASE_URL", cfg.LLMBaseURL)
	cfg.LLMAPIKey = getEnv("LIFEOS_LLM_API_KEY", cfg.LLMAPIKey)
	cfg.LLMModel = getEnv("LIFEOS_LLM_MODEL", cfg.LLMModel)
	cfg.MCPEnabled = getEnvBool("LIFEOS_MCP_ENABLED", cfg.MCPEnabled)
	cfg.MCPPath = getEnv("LIFEOS_MCP_PATH", cfg.MCPPath)

	// Validate timezone using embedded IANA database
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, ErrInvalidTimezone{cfg.Timezone}
	}
	_ = loc // validated

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "lifeos.db")
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultValue
	}
	return b
}

type ErrInvalidTimezone struct {
	TZ string
}

func (e ErrInvalidTimezone) Error() string {
	return "invalid timezone: " + e.TZ
}

func loadDotEnv(path string) error {
	// Simple .env loader - reads KEY=VALUE lines
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env file is optional
		}
		return err
	}

	lines := string(data)
	for _, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove surrounding quotes if present
		value = strings.Trim(value, "\"'")
		// Only set if not already set in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return nil
}
