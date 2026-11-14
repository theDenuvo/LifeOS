package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

// ProviderError is a failed LLM request.
//
// A provider outage is not a user-facing message failure: callers inspect
// Status and Retryable to decide between retrying the same model and falling
// back to another one.
type ProviderError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
	// Transient is set for in-stream provider errors that carry no HTTP status
	// but clearly describe an overload or a rate limit.
	Transient bool
}

func (e *ProviderError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("llm: status %d: %s", e.Status, e.Body)
	}
	return "llm: " + e.Body
}

// Retryable reports whether the same request may succeed later.
func (e *ProviderError) Retryable() bool {
	if e.Transient {
		return true
	}
	switch e.Status {
	case 0:
		return false
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return e.Status >= 500 && e.Status <= 599
}

// RetryPolicy controls retries and model fallback.
type RetryPolicy struct {
	// AttemptsPerModel is how many times one model is tried before moving on.
	AttemptsPerModel int
	// MaxModels caps how many models one turn may try, including the selected one.
	MaxModels int
	// BaseDelay is the first backoff step; it doubles per attempt.
	BaseDelay time.Duration
	// MaxDelay caps a single backoff step.
	MaxDelay time.Duration
}

// DefaultRetryPolicy is the policy used by the assistant runtime.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		AttemptsPerModel: 3,
		MaxModels:        3,
		BaseDelay:        400 * time.Millisecond,
		MaxDelay:         4 * time.Second,
	}
}

// RetryProvider wraps a provider factory with retries and model fallback.
//
// The selected model is tried first. When it keeps failing, the chain moves to
// the fallback models, so one rate-limited free model does not end the turn.
type RetryProvider struct {
	newProvider func(model string) assistant.Provider
	fallbacks   func(ctx context.Context) []string
	policy      RetryPolicy
	observer    func(assistant.ProviderRetry)
	model       string
}

// NewRetryProvider builds a resilient provider over a model factory.
//
// fallbacks is optional and evaluated per request, which lets the catalog
// supply currently available models.
func NewRetryProvider(newProvider func(model string) assistant.Provider, fallbacks func(ctx context.Context) []string, policy RetryPolicy) *RetryProvider {
	if policy.AttemptsPerModel <= 0 {
		policy.AttemptsPerModel = 1
	}
	if policy.MaxModels <= 0 {
		policy.MaxModels = 1
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = 400 * time.Millisecond
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = 4 * time.Second
	}
	return &RetryProvider{newProvider: newProvider, fallbacks: fallbacks, policy: policy}
}

// WithModel binds the provider to a model; fallbacks stay available.
func (p *RetryProvider) WithModel(model string) assistant.Provider {
	if model == "" {
		model = p.model
	}
	clone := *p
	clone.model = model
	return &clone
}

// WithRetryObserver returns a copy that reports retries and fallbacks.
func (p *RetryProvider) WithRetryObserver(observer func(assistant.ProviderRetry)) assistant.Provider {
	clone := *p
	clone.observer = observer
	return &clone
}

// Complete performs a non-streaming completion with retries and fallback.
func (p *RetryProvider) Complete(ctx context.Context, messages []assistant.ChatMessage, tools []assistant.ToolSpec) (assistant.Completion, error) {
	var lastErr error
	used := make([]string, 0, p.policy.MaxModels)
	for {
		model, ok := p.nextModel(ctx, used)
		if !ok {
			break
		}
		used = append(used, model)
		for attempt := 1; attempt <= p.policy.AttemptsPerModel; attempt++ {
			completion, err := p.newProvider(model).Complete(ctx, messages, tools)
			if err == nil {
				return completion, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				return assistant.Completion{}, err
			}
			if !RetryableError(err) || attempt == p.policy.AttemptsPerModel {
				break
			}
			p.report(model, attempt, false, err)
			if !sleepContext(ctx, p.delay(attempt)) {
				return assistant.Completion{}, ctx.Err()
			}
		}
		p.report(model, p.policy.AttemptsPerModel, true, lastErr)
	}
	if lastErr == nil {
		lastErr = errors.New("llm: no model available")
	}
	return assistant.Completion{}, lastErr
}

// StreamComplete streams with retries and fallback.
//
// A stream that already delivered text is never retried: the user is watching
// that answer, and silently restarting it would duplicate output. The caller
// keeps the partial text and decides what to show.
func (p *RetryProvider) StreamComplete(ctx context.Context, messages []assistant.ChatMessage, tools []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	var lastErr error
	used := make([]string, 0, p.policy.MaxModels)
	for {
		model, ok := p.nextModel(ctx, used)
		if !ok {
			break
		}
		used = append(used, model)
		for attempt := 1; attempt <= p.policy.AttemptsPerModel; attempt++ {
			emitted := false
			streamed := func(delta assistant.StreamDelta) error {
				emitted = true
				if onDelta == nil {
					return nil
				}
				return onDelta(delta)
			}
			completion, err := p.streamOnce(ctx, model, messages, tools, streamed)
			if err == nil && !emptyCompletion(completion) {
				return completion, nil
			}
			if err == nil {
				err = &ProviderError{Body: "пустой ответ модели", Transient: true}
			}
			lastErr = err
			if ctx.Err() != nil {
				return assistant.Completion{}, err
			}
			// Text is already on screen: never restart it under the user.
			if emitted || !RetryableError(err) || attempt == p.policy.AttemptsPerModel {
				break
			}
			p.report(model, attempt, false, err)
			if !sleepContext(ctx, p.delay(attempt)) {
				return assistant.Completion{}, ctx.Err()
			}
		}
		p.report(model, p.policy.AttemptsPerModel, true, lastErr)
	}
	if lastErr == nil {
		lastErr = errors.New("llm: no model available")
	}
	return assistant.Completion{}, lastErr
}

func (p *RetryProvider) streamOnce(ctx context.Context, model string, messages []assistant.ChatMessage, tools []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	provider := p.newProvider(model)
	if streamer, ok := provider.(assistant.StreamingProvider); ok {
		return streamer.StreamComplete(ctx, messages, tools, onDelta)
	}
	completion, err := provider.Complete(ctx, messages, tools)
	if err != nil {
		return assistant.Completion{}, err
	}
	if onDelta != nil {
		if completion.Reasoning != "" {
			if deltaErr := onDelta(assistant.StreamDelta{Reasoning: completion.Reasoning}); deltaErr != nil {
				return assistant.Completion{}, deltaErr
			}
		}
		if completion.Content != "" {
			if deltaErr := onDelta(assistant.StreamDelta{Content: completion.Content}); deltaErr != nil {
				return assistant.Completion{}, deltaErr
			}
		}
	}
	return completion, nil
}

// nextModel returns the next model to try. The selected model comes first; the
// fallback list is only consulted once the previous model is exhausted, because
// it may have to reach the network.
func (p *RetryProvider) nextModel(ctx context.Context, used []string) (string, bool) {
	if len(used) >= p.policy.MaxModels {
		return "", false
	}
	if len(used) == 0 {
		return p.model, true
	}
	if p.fallbacks == nil {
		return "", false
	}
	for _, model := range p.fallbacks(ctx) {
		candidate := strings.TrimSpace(model)
		if candidate == "" {
			continue
		}
		skip := false
		for _, existing := range used {
			if existing == candidate {
				skip = true
				break
			}
		}
		if !skip {
			return candidate, true
		}
	}
	return "", false
}

func (p *RetryProvider) delay(attempt int) time.Duration {
	delay := p.policy.BaseDelay
	for step := 1; step < attempt; step++ {
		delay *= 2
	}
	if delay > p.policy.MaxDelay {
		return p.policy.MaxDelay
	}
	return delay
}

func (p *RetryProvider) report(model string, attempt int, fallback bool, err error) {
	if p.observer == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	p.observer(assistant.ProviderRetry{
		Model:    model,
		Attempt:  attempt,
		Fallback: fallback,
		Reason:   reason,
	})
}

// RetryableError reports whether a provider failure may succeed on a retry.
func RetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable()
	}
	// Transport-level failures: connection reset, DNS hiccup, truncated body.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	return false
}

func emptyCompletion(completion assistant.Completion) bool {
	return strings.TrimSpace(completion.Content) == "" &&
		strings.TrimSpace(completion.Reasoning) == "" &&
		len(completion.ToolCalls) == 0
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
