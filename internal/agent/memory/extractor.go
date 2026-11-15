// Package memory extracts durable personal facts and reusable knowledge from
// conversations and documents with an OpenAI-compatible model.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

const (
	maxResponse   = 4 << 20
	maxCandidates = 20
)

var fencedJSON = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// Extractor calls an OpenAI-compatible chat-completions endpoint.
type Extractor struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewExtractor builds an extractor. An empty model disables extraction; callers
// treat that as "memory stays manual" rather than an error.
func NewExtractor(baseURL, apiKey, model string) *Extractor {
	return &Extractor{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   strings.TrimSpace(model),
		client:  &http.Client{Timeout: 90 * time.Second},
	}
}

// Enabled reports whether the extractor can call a model.
func (e *Extractor) Enabled() bool {
	return e != nil && e.model != "" && e.baseURL != ""
}

// ExtractConversation learns durable statements from one exchange.
func (e *Extractor) ExtractConversation(ctx context.Context, text string) ([]appmemory.Candidate, error) {
	if !e.Enabled() {
		return nil, nil
	}
	prompt := fmt.Sprintf(`Ты ведёшь долговременную память личной системы LifeOS.
Из фрагмента диалога извлеки только устойчивые сведения о пользователе и его мире, которые пригодятся позже.
Верни только JSON:
{"items":[{"title":"краткая тема, 1-8 слов","content":"одно ясное утверждение","category":"preference|habit|circumstance|fact|agreement|context|rule","certainty":"fact|assumption|inference","confidence":0.0,"entities":["имя, проект, место"],"evidence":"короткая цитата","correction":false}]}

Правила:
- Не сохраняй разовые действия, планы на сегодня, задачи, пересказ инструментов и пустые любезности.
- Сохраняй предпочтения, привычки, обстоятельства, договорённости, устойчивые правила, важные факты и контекст деятельности.
- Не выдумывай. Если пользователь не утверждал это, не сохраняй.
- Предположение или вывод помечай certainty=assumption или inference и снижай confidence.
- correction=true только если пользователь явно исправляет ранее известное сведение.
- Не больше 8 сведений. Если сохранять нечего, верни {"items":[]}.

Фрагмент диалога:
%s`, text)
	return e.extract(ctx, prompt)
}

// ExtractDocument learns facts and entities from a document. The document text
// itself is indexed separately, so this focuses on reusable statements.
func (e *Extractor) ExtractDocument(ctx context.Context, filename, text string) ([]appmemory.Candidate, error) {
	if !e.Enabled() {
		return nil, nil
	}
	prompt := fmt.Sprintf(`Ты ведёшь базу знаний личной системы LifeOS.
Из документа извлеки самостоятельные полезные сведения и справочные материалы.
Верни только JSON:
{"items":[{"title":"краткая тема","content":"сведение или инструкция","category":"instruction|summary|reference|research|idea|documentation|fact|agreement|rule","certainty":"fact|assumption|inference","confidence":0.0,"entities":["имена, проекты, термины"],"evidence":"цитата из документа","correction":false}]}

Правила:
- Сохраняй только то, что можно понять без остального документа.
- Не пересказывай документ целиком и не создавай задачи.
- Ничего не выдумывай; при сомнении ставь certainty=assumption.
- Не больше 12 сведений. Если извлекать нечего, верни {"items":[]}.

Документ: %s
%s`, filename, text)
	return e.extract(ctx, prompt)
}

type chatPayload struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

func (e *Extractor) extract(ctx context.Context, prompt string) ([]appmemory.Candidate, error) {
	body, err := json.Marshal(chatPayload{
		Model: e.model,
		Messages: []message{
			{Role: "system", Content: "Ты аккуратный хранитель памяти. Отвечай только валидным JSON без пояснений."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   2048,
		Temperature: 0.1,
	})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		text, err := e.do(ctx, body)
		if err == nil {
			return parseCandidates(text)
		}
		lastErr = err
	}
	return nil, lastErr
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
	request.Header.Set("X-Title", "LifeOS Memory Extractor")

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
		return "", fmt.Errorf("memory llm: status %d", response.StatusCode)
	}
	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("memory llm: decode response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return "", fmt.Errorf("memory llm: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", errors.New("memory llm: empty response")
	}
	return contentText(decoded.Choices[0].Message.Content)
}

func contentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("memory llm: empty content")
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
	return "", errors.New("memory llm: unsupported content")
}

type rawCandidate struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Category   string   `json:"category"`
	Certainty  string   `json:"certainty"`
	Confidence float64  `json:"confidence"`
	Entities   []string `json:"entities"`
	Evidence   string   `json:"evidence"`
	Correction bool     `json:"correction"`
}

func parseCandidates(value string) ([]appmemory.Candidate, error) {
	value = strings.TrimSpace(value)
	if match := fencedJSON.FindStringSubmatch(value); len(match) == 2 {
		value = strings.TrimSpace(match[1])
	}
	start := strings.Index(value, "{")
	if start < 0 {
		return nil, fmt.Errorf("memory llm: no JSON in response")
	}
	value = value[start:]
	if end := strings.LastIndex(value, "}"); end >= 0 {
		value = value[:end+1]
	}
	var object struct {
		Items []rawCandidate `json:"items"`
	}
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		return nil, fmt.Errorf("memory llm: invalid JSON: %w", err)
	}
	result := make([]appmemory.Candidate, 0, len(object.Items))
	for _, item := range object.Items {
		category := domainmemory.Category(strings.ToLower(strings.TrimSpace(item.Category)))
		if !category.IsValid() {
			continue
		}
		certainty := domainmemory.Certainty(strings.ToLower(strings.TrimSpace(item.Certainty)))
		if !certainty.IsValid() {
			certainty = domainmemory.CertaintyAssumption
		}
		confidence := item.Confidence
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}
		// A free-form model may claim certainty it cannot have. Inferences never
		// enter memory with high confidence.
		if certainty == domainmemory.CertaintyInference && confidence > 0.7 {
			confidence = 0.7
		}
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		result = append(result, appmemory.Candidate{
			Title: item.Title, Content: item.Content, Category: category,
			Certainty: certainty, Confidence: confidence, Entities: item.Entities,
			Evidence: item.Evidence, Correction: item.Correction,
		})
		if len(result) >= maxCandidates {
			break
		}
	}
	return result, nil
}
