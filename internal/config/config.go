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

	// Resilience: retries per model, extra models to fall back to, and the
	// backoff base. One flaky free model must not end the turn.
	LLMFallbackModels []string
	LLMRetryAttempts  int
	LLMRetryDelayMS   int

	// AssistantMaxRounds caps tool rounds per turn; bulk imports need headroom.
	AssistantMaxRounds int

	DocLLMBaseURL string
	DocLLMAPIKey  string
	DocLLMModel   string
	DocumentsDir  string

	MCPEnabled     bool
	MCPPath        string
	MCPAllowWrites bool

	// ToolsRoot restricts the local file tools to one directory. An empty root
	// means the assistant may work with any path on the machine.
	ToolsRoot  string
	ToolsShell bool
}

func Load(configPath string) (*Config, error) {
	// Default to .env if not specified
	if configPath == "" {
		configPath = ".env"
	}

	cfg := &Config{
		ListenAddr:         getEnv("LIFEOS_LISTEN_ADDR", "127.0.0.1:8787"),
		DataDir:            getEnv("LIFEOS_DATA_DIR", "./data"),
		Timezone:           getEnv("LIFEOS_TIMEZONE", "Europe/Moscow"),
		LogLevel:           getEnv("LIFEOS_LOG_LEVEL", "info"),
		LLMProvider:        getEnv("LIFEOS_LLM_PROVIDER", "openrouter"),
		LLMBaseURL:         getEnv("LIFEOS_LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMAPIKey:          getEnv("LIFEOS_LLM_API_KEY", ""),
		LLMModel:           getEnv("LIFEOS_LLM_MODEL", "openai/gpt-4o-mini"),
		LLMRetryAttempts:   getEnvInt("LIFEOS_LLM_RETRY_ATTEMPTS", 3),
		LLMRetryDelayMS:    getEnvInt("LIFEOS_LLM_RETRY_DELAY_MS", 400),
		AssistantMaxRounds: getEnvInt("LIFEOS_ASSISTANT_MAX_ROUNDS", 12),
		DocLLMBaseURL:      getEnv("LIFEOS_DOC_LLM_BASE_URL", ""),
		DocLLMAPIKey:       getEnv("LIFEOS_DOC_LLM_API_KEY", ""),
		DocLLMModel:        getEnv("LIFEOS_DOC_LLM_MODEL", ""),
		MCPEnabled:         getEnvBool("LIFEOS_MCP_ENABLED", false),
		MCPPath:            getEnv("LIFEOS_MCP_PATH", "/mcp"),
		MCPAllowWrites:     getEnvBool("LIFEOS_MCP_ALLOW_WRITES", false),
		ToolsRoot:          getEnv("LIFEOS_TOOLS_ROOT", ""),
		ToolsShell:         getEnvBool("LIFEOS_TOOLS_SHELL", true),
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
	cfg.LLMFallbackModels = getEnvList("LIFEOS_LLM_FALLBACK_MODELS", cfg.LLMFallbackModels)
	cfg.LLMRetryAttempts = getEnvInt("LIFEOS_LLM_RETRY_ATTEMPTS", cfg.LLMRetryAttempts)
	cfg.LLMRetryDelayMS = getEnvInt("LIFEOS_LLM_RETRY_DELAY_MS", cfg.LLMRetryDelayMS)
	cfg.AssistantMaxRounds = getEnvInt("LIFEOS_ASSISTANT_MAX_ROUNDS", cfg.AssistantMaxRounds)
	cfg.DocLLMBaseURL = getEnv("LIFEOS_DOC_LLM_BASE_URL", cfg.LLMBaseURL)
	cfg.DocLLMAPIKey = getEnv("LIFEOS_DOC_LLM_API_KEY", cfg.LLMAPIKey)
	cfg.DocLLMModel = getEnv("LIFEOS_DOC_LLM_MODEL", defaultDocModel(cfg.DocLLMBaseURL, cfg.LLMModel))
	cfg.DocumentsDir = getEnv("LIFEOS_DOCUMENTS_DIR", filepath.Join(cfg.DataDir, "documents"))
	cfg.MCPEnabled = getEnvBool("LIFEOS_MCP_ENABLED", cfg.MCPEnabled)
	cfg.MCPPath = getEnv("LIFEOS_MCP_PATH", cfg.MCPPath)
	cfg.MCPAllowWrites = getEnvBool("LIFEOS_MCP_ALLOW_WRITES", cfg.MCPAllowWrites)
	cfg.ToolsRoot = getEnv("LIFEOS_TOOLS_ROOT", cfg.ToolsRoot)
	cfg.ToolsShell = getEnvBool("LIFEOS_TOOLS_SHELL", cfg.ToolsShell)

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
	if err := os.MkdirAll(cfg.DocumentsDir, 0750); err != nil {
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

func getEnvInt(key string, defaultValue int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

// getEnvList reads a comma-separated list, trimming blanks.
func getEnvList(key string, defaultValue []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	parts := make([]string, 0, 4)
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return defaultValue
	}
	return parts
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

// defaultDocModel prefers a free vision-capable model when OpenRouter is used.
// The user can override it with LIFEOS_DOC_LLM_MODEL.
func defaultDocModel(baseURL, fallback string) string {
	if strings.Contains(strings.ToLower(baseURL), "openrouter.ai") {
		return "particle-ai/particle-1:free"
	}
	return fallback
}
