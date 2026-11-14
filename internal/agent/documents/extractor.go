// Package documents extracts task candidates from document content with an
// OpenAI-compatible vision/text model.
package documents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
)

const (
	maxChunkRunes = 24000
	maxChunks     = 8
	maxResponse   = 8 << 20
)

var fencedJSON = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// Extractor calls an OpenAI-compatible chat-completions endpoint.
type Extractor struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewExtractor builds an extractor. The model is used for text and images.
func NewExtractor(baseURL, apiKey, model string) *Extractor {
	return &Extractor{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// ExtractTasks processes text in chunks or one image.
func (e *Extractor) ExtractTasks(ctx context.Context, input appdocuments.TaskExtractionInput) ([]appdocuments.ExtractedTask, error) {
	if e.model == "" || e.baseURL == "" {
		return nil, appdocuments.ErrLLMNotConfigured
	}
	if strings.Contains(strings.ToLower(e.baseURL), "openrouter.ai") && !isFreeModel(e.model) {
		return nil, fmt.Errorf("OpenRouter: разрешены только бесплатные модели (суффикс :free), выбрана %q", e.model)
	}
	if len(input.Image) > 0 {
		return e.extractOne(ctx, e.imagePrompt(input), input.Image, input.ImageMIME)
	}

	chunks := splitText(input.Text, maxChunkRunes, maxChunks)
	if len(chunks) == 0 {
		return nil, errors.New("в документе не найден текст для извлечения задач")
	}
	result := make([]appdocuments.ExtractedTask, 0)
	seen := make(map[string]struct{})
	for index, chunk := range chunks {
		tasks, err := e.extractOne(ctx, e.textPrompt(input, chunk, index+1, len(chunks)), nil, "")
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			key := strings.ToLower(strings.TrimSpace(task.Title))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, task)
		}
	}
	return result, nil
}

func (e *Extractor) textPrompt(input appdocuments.TaskExtractionInput, chunk string, part, total int) string {
	return fmt.Sprintf(`Ты извлекаешь задачи из документа LifeOS.
Файл: %s. Сегодня: %s (%s). Часть %d из %d.
Верни только JSON вида:
{"tasks":[{"title":"краткая задача","note":"контекст или null","due_date":"YYYY-MM-DD или null","priority":"low|medium|high","confidence":0.0,"source":"короткая цитата"}]}
Правила:
- Извлекай только реальные поручения, дела, встречи и сроки. Не выдумывай задачи.
- Если срок не указан, due_date = null. Не угадывай год, если он не следует из текста.
- Если приоритет не указан, используй medium.
- confidence — уверенность от 0 до 1.
- source — короткая цитата из документа.
- Не больше 40 задач. Если задач нет, верни {"tasks":[]}.

Текст документа:
%s`, input.Filename, input.Today.Format("2006-01-02"), input.Timezone, part, total, chunk)
}

func (e *Extractor) imagePrompt(input appdocuments.TaskExtractionInput) string {
	return fmt.Sprintf(`Ты извлекаешь задачи из фотографии или скана документа LifeOS.
Файл: %s. Сегодня: %s (%s).
Верни только JSON вида:
{"tasks":[{"title":"краткая задача","note":"контекст или null","due_date":"YYYY-MM-DD или null","priority":"low|medium|high","confidence":0.0,"source":"короткая цитата"}]}
Прочитай текст на изображении. Извлекай только реальные поручения, дела, встречи и сроки. Не выдумывай. Если задач нет, верни {"tasks":[]}.`, input.Filename, input.Today.Format("2006-01-02"), input.Timezone)
}

type chatPayload struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *Extractor) extractOne(ctx context.Context, prompt string, image []byte, imageMIME string) ([]appdocuments.ExtractedTask, error) {
	content := any(prompt)
	if len(image) > 0 {
		if imageMIME == "" {
			imageMIME = "image/jpeg"
		}
		content = []map[string]any{
			{"type": "text", "text": prompt},
			{"type": "image_url", "image_url": map[string]any{
				"url": "data:" + imageMIME + ";base64," + base64.StdEncoding.EncodeToString(image),
			}},
		}
	}
	body, err := json.Marshal(chatPayload{
		Model: e.model,
		Messages: []message{
			{Role: "system", Content: "Ты аккуратный извлекатель задач. Отвечай только валидным JSON без пояснений."},
			{Role: "user", Content: content},
		},
		MaxTokens:   4096,
		Temperature: 0.1,
	})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		text, err := e.do(ctx, body)
		if err == nil {
			return parseTasks(text)
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return nil, lastErr
}

// ConvertToMarkdown turns an image (or an already extracted text document)
// into Markdown for the chat model. Conversion is separate from task
// extraction: the chat model decides what to do with the document later.
func (e *Extractor) ConvertToMarkdown(ctx context.Context, input appdocuments.TaskExtractionInput) (string, error) {
	if strings.TrimSpace(input.Text) != "" {
		return input.Text, nil
	}
	if len(input.Image) == 0 {
		return "", errors.New("в документе нет текста или изображения")
	}
	if e.model == "" || e.baseURL == "" {
		return "", appdocuments.ErrLLMNotConfigured
	}
	if strings.Contains(strings.ToLower(e.baseURL), "openrouter.ai") && !isFreeModel(e.model) {
		return "", fmt.Errorf("OpenRouter: разрешены только бесплатные модели")
	}
	imageMIME := input.ImageMIME
	if imageMIME == "" {
		imageMIME = "image/jpeg"
	}
	prompt := fmt.Sprintf(`Преобразуй изображение документа в Markdown.
Сохрани весь видимый текст, заголовки, списки, таблицы и структуру. Не добавляй свои комментарии, не извлекай задачи и не сокращай содержимое. Если текста нет, верни пустую строку.
Файл: %s`, input.Filename)
	body, err := json.Marshal(chatPayload{
		Model: e.model,
		Messages: []message{
			{Role: "system", Content: "Ты точный OCR-конвертер в Markdown. Отвечай только Markdown."},
			{Role: "user", Content: []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]any{
					"url": "data:" + imageMIME + ";base64," + base64.StdEncoding.EncodeToString(input.Image),
				}},
			}},
		},
		MaxTokens:   8192,
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		text, err := e.do(ctx, body)
		if err == nil {
			return strings.TrimSpace(text), nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return "", lastErr
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("document llm: status %d: %s", e.status, e.body)
}

func retryable(err error) bool {
	var value *httpError
	if errors.As(err, &value) {
		return value.status == http.StatusTooManyRequests || value.status >= 500
	}
	return false
}

func (e *Extractor) do(ctx context.Context, body []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	request.Header.Set("HTTP-Referer", "https://lifeos.local")
	request.Header.Set("X-Title", "LifeOS Document Extractor")

	response, err := e.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		text := strings.TrimSpace(string(payload))
		if len(text) > 600 {
			text = text[:600]
		}
		return "", &httpError{status: response.StatusCode, body: text}
	}

	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("document llm: decode response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return "", fmt.Errorf("document llm: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", errors.New("document llm: empty response")
	}
	text, err := contentText(decoded.Choices[0].Message.Content)
	if err != nil {
		return "", err
	}
	return text, nil
}

func contentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("document llm: empty content")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		if builder.Len() > 0 {
			return builder.String(), nil
		}
	}
	return "", errors.New("document llm: unsupported content")
}

func isFreeModel(model string) bool {
	return model == "openrouter/free" || strings.HasSuffix(model, ":free")
}

func parseTasks(value string) ([]appdocuments.ExtractedTask, error) {
	value = strings.TrimSpace(value)
	if match := fencedJSON.FindStringSubmatch(value); len(match) == 2 {
		value = strings.TrimSpace(match[1])
	}
	start := strings.IndexAny(value, "{[")
	if start < 0 {
		return nil, fmt.Errorf("document llm: no JSON in response: %s", truncate(value, 200))
	}
	value = value[start:]
	if strings.HasPrefix(value, "{") {
		if end := strings.LastIndex(value, "}"); end >= 0 {
			value = value[:end+1]
		}
	} else if end := strings.LastIndex(value, "]"); end >= 0 {
		value = value[:end+1]
	}
	var direct []appdocuments.ExtractedTask
	if strings.HasPrefix(value, "[") {
		if err := json.Unmarshal([]byte(value), &direct); err == nil {
			return direct, nil
		}
	}
	var object struct {
		Tasks []appdocuments.ExtractedTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		return nil, fmt.Errorf("document llm: invalid JSON: %w: %s", err, truncate(value, 200))
	}
	return object.Tasks, nil
}

func splitText(value string, limit, maxChunks int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	runes := []rune(value)
	chunks := make([]string, 0, maxChunks)
	for len(runes) > 0 && len(chunks) < maxChunks {
		size := limit
		if len(runes) < size {
			size = len(runes)
		}
		cut := size
		if size < len(runes) {
			window := string(runes[:size])
			if index := strings.LastIndex(window, "\n\n"); index > size/2 {
				cut = len([]rune(window[:index]))
			}
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	return chunks
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
