package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelInfo is one selectable assistant model.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
}

// ModelCatalog lists free models for the picker and validates selections.
type ModelCatalog struct {
	baseURL  string
	apiKey   string
	fallback string
	client   *http.Client

	mu        sync.Mutex
	cached    []ModelInfo
	fetchedAt time.Time
}

// NewModelCatalog builds a catalog. The fallback model is always available.
func NewModelCatalog(baseURL, apiKey, fallback string) *ModelCatalog {
	return &ModelCatalog{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		fallback: fallback,
		client:   &http.Client{Timeout: 20 * time.Second},
	}
}

type modelsResponse struct {
	Data []struct {
		ID                  string   `json:"id"`
		Name                string   `json:"name"`
		ContextLength       int      `json:"context_length"`
		SupportedParameters []string `json:"supported_parameters"`
		Pricing             struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
	} `json:"data"`
}

// List returns free, tool-capable models. On provider errors it returns the
// configured fallback model so the picker still works.
func (c *ModelCatalog) List(ctx context.Context) []ModelInfo {
	c.mu.Lock()
	if len(c.cached) > 0 && time.Since(c.fetchedAt) < 10*time.Minute {
		result := append([]ModelInfo(nil), c.cached...)
		c.mu.Unlock()
		return result
	}
	c.mu.Unlock()

	if !strings.Contains(strings.ToLower(c.baseURL), "openrouter.ai") || c.apiKey == "" {
		return c.fallbackOnly()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return c.fallbackOnly()
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.client.Do(request)
	if err != nil {
		return c.fallbackOnly()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.fallbackOnly()
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return c.fallbackOnly()
	}
	var decoded modelsResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return c.fallbackOnly()
	}
	models := make([]ModelInfo, 0, len(decoded.Data))
	// The assistant drives LifeOS through tools. A model without tool calling can
	// chat but cannot act, so it is kept out of the picker and out of the
	// fallback chain whenever tool-capable models are advertised.
	toolCapable := make([]ModelInfo, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		if item.ID == "" || !isZeroPrice(item.Pricing.Prompt) || !isZeroPrice(item.Pricing.Completion) {
			continue
		}
		if !strings.HasSuffix(item.ID, ":free") || strings.Contains(item.ID, ":batch") {
			continue
		}
		name := item.Name
		if name == "" {
			name = item.ID
		}
		entry := ModelInfo{ID: item.ID, Name: name, ContextLength: item.ContextLength}
		models = append(models, entry)
		if supportsTools(item.SupportedParameters) {
			toolCapable = append(toolCapable, entry)
		}
	}
	if len(toolCapable) > 0 && len(toolCapable) < len(models) {
		models = toolCapable
	}
	if c.fallback != "" && strings.HasSuffix(c.fallback, ":free") && !containsModel(models, c.fallback) {
		models = append(models, ModelInfo{ID: c.fallback, Name: c.fallback})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].ID == "openrouter/free" {
			return true
		}
		if models[j].ID == "openrouter/free" {
			return false
		}
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
	c.mu.Lock()
	c.cached = models
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return append([]ModelInfo(nil), models...)
}

// ValidateFree rejects OpenRouter models without the :free suffix.
func (c *ModelCatalog) ValidateFree(ctx context.Context, model string) error {
	if model == "" || !strings.Contains(strings.ToLower(c.baseURL), "openrouter.ai") {
		return nil
	}
	if strings.HasSuffix(model, ":free") {
		return nil
	}
	return errors.New("в OpenRouter разрешены только модели с суффиксом :free")
}

func (c *ModelCatalog) fallbackOnly() []ModelInfo {
	if c.fallback == "" {
		return nil
	}
	return []ModelInfo{{ID: c.fallback, Name: c.fallback}}
}

func isZeroPrice(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "0", "0.0", "0.00", "0.000000":
		return true
	default:
		return false
	}
}

func supportsTools(parameters []string) bool {
	for _, value := range parameters {
		if value == "tools" {
			return true
		}
	}
	return false
}

func containsModel(models []ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

// Describe returns a human name for a model id.
func Describe(models []ModelInfo, id string) string {
	for _, model := range models {
		if model.ID == id {
			return model.Name
		}
	}
	if id == "" {
		return "По умолчанию"
	}
	return id
}
