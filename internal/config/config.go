package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr    string
	DataDir       string
	Timezone      string
	LogLevel      string

	LLMProvider   string
	LLMBaseURL    string
	LLMAPIKey     string
	LLMModel      string

	MCPEnabled    bool
	MCPPath       string
}

func Load(configPath string) (*Config, error) {
	cfg := &Config{
		ListenAddr:  getEnv("LIFEOS_LISTEN_ADDR", "127.0.0.1:8787"),
		DataDir:     getEnv("LIFEOS_DATA_DIR", "./data"),
		Timezone:    getEnv("LIFEOS_TIMEZONE", "Europe/Moscow"),
		LogLevel:    getEnv("LIFEOS_LOG_LEVEL", "info"),
		LLMProvider: getEnv("LIFEOS_LLM_PROVIDER", "openrouter"),
		LLMBaseURL:  getEnv("LIFEOS_LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMAPIKey:   getEnv("LIFEOS_LLM_API_KEY", ""),
		LLMModel:    getEnv("LIFEOS_LLM_MODEL", "openai/gpt-4o-mini"),
		MCPEnabled:  getEnvBool("LIFEOS_MCP_ENABLED", true),
		MCPPath:     getEnv("LIFEOS_MCP_PATH", "/mcp"),
	}

	// Validate timezone - on Windows, use fixed offset if IANA not available
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		// Fallback for Windows: Moscow is UTC+3 year-round (no DST since 2014)
		if cfg.Timezone == "Europe/Moscow" {
			loc = time.FixedZone("MSK", 3*60*60)
		} else {
			return nil, ErrInvalidTimezone{cfg.Timezone}
		}
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