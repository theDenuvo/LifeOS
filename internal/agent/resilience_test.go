package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

type fakeProvider struct {
	mu        sync.Mutex
	completes int
	streams   int
	failures  int
	err       error
	deltas    []string
	answer    string
}

func (p *fakeProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completes++
	if p.failures > 0 {
		p.failures--
		return assistant.Completion{}, p.err
	}
	return assistant.Completion{Content: p.answer}, nil
}

func (p *fakeProvider) StreamComplete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	p.mu.Lock()
	p.streams++
	failures := p.failures
	if failures > 0 {
		p.failures--
	}
	err := p.err
	p.mu.Unlock()

	if failures > 0 {
		return assistant.Completion{}, err
	}
	for _, delta := range p.deltas {
		if onDelta != nil {
			if deltaErr := onDelta(assistant.StreamDelta{Content: delta}); deltaErr != nil {
				return assistant.Completion{}, deltaErr
			}
		}
	}
	return assistant.Completion{Content: p.answer}, nil
}

func (p *fakeProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.completes, p.streams
}

func testPolicy() RetryPolicy {
	return RetryPolicy{AttemptsPerModel: 3, MaxModels: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func TestRetryProviderRetriesTransientFailureOnSameModel(t *testing.T) {
	primary := &fakeProvider{failures: 2, err: &ProviderError{Status: http.StatusTooManyRequests, Body: "rate limited"}, answer: "готово"}
	retries := 0
	provider := NewRetryProvider(func(string) assistant.Provider { return primary },
		func(context.Context) []string { return []string{"fallback/model:free"} }, testPolicy()).
		WithRetryObserver(func(assistant.ProviderRetry) { retries++ })

	completion, err := provider.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completion.Content != "готово" {
		t.Fatalf("unexpected completion: %+v", completion)
	}
	if calls, _ := primary.calls(); calls != 3 {
		t.Fatalf("expected three attempts on the same model, got %d", calls)
	}
	if retries != 2 {
		t.Fatalf("expected two retry notifications, got %d", retries)
	}
}

func TestRetryProviderFallsBackToAnotherModel(t *testing.T) {
	primary := &fakeProvider{failures: 99, err: &ProviderError{Status: http.StatusServiceUnavailable, Body: "upstream down"}}
	secondary := &fakeProvider{answer: "ответ запасной модели"}

	provider := NewRetryProvider(func(model string) assistant.Provider {
		if model == "primary:free" {
			return primary
		}
		return secondary
	}, func(context.Context) []string { return []string{"secondary:free"} }, testPolicy()).
		WithModel("primary:free")

	events := make([]assistant.ProviderRetry, 0, 4)
	observer, ok := provider.(assistant.RetryObserverProvider)
	if !ok {
		t.Fatal("retry provider must expose a retry observer")
	}
	provider = observer.WithRetryObserver(func(event assistant.ProviderRetry) { events = append(events, event) })

	completion, err := provider.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("complete with fallback: %v", err)
	}
	if completion.Content != "ответ запасной модели" {
		t.Fatalf("fallback answer missing: %+v", completion)
	}
	if calls, _ := primary.calls(); calls != 3 {
		t.Fatalf("primary model must be retried before fallback, got %d attempts", calls)
	}
	fallbackNotified := false
	for _, event := range events {
		if event.Fallback && event.Model == "primary:free" {
			fallbackNotified = true
		}
	}
	if !fallbackNotified {
		t.Fatalf("fallback was not reported: %+v", events)
	}
}

func TestRetryProviderDoesNotRetryPermanentFailure(t *testing.T) {
	primary := &fakeProvider{failures: 99, err: &ProviderError{Status: http.StatusUnauthorized, Body: "bad key"}}
	provider := NewRetryProvider(func(string) assistant.Provider { return primary }, nil, testPolicy())

	if _, err := provider.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if calls, _ := primary.calls(); calls != 1 {
		t.Fatalf("authentication failure must not be retried, got %d attempts", calls)
	}
}

func TestStreamRetriesOnlyBeforeFirstDelta(t *testing.T) {
	primary := &fakeProvider{failures: 1, err: &ProviderError{Status: http.StatusBadGateway, Body: "gateway"}, answer: "готово", deltas: []string{"го", "тово"}}
	provider := NewRetryProvider(func(string) assistant.Provider { return primary }, nil, testPolicy())

	chunks := make([]string, 0, 4)
	completion, err := provider.StreamComplete(context.Background(), nil, nil, func(delta assistant.StreamDelta) error {
		chunks = append(chunks, delta.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(chunks) != 2 || completion.Content != "готово" {
		t.Fatalf("unexpected stream result: %v %+v", chunks, completion)
	}
	if _, streams := primary.calls(); streams != 2 {
		t.Fatalf("expected a retry before any delta, got %d streams", streams)
	}
}

func TestStreamDoesNotRestartAfterVisibleText(t *testing.T) {
	// The provider emits one delta and then fails: the user already sees text,
	// so the runtime must surface the failure instead of silently restarting.
	partial := &partialFailProvider{}
	provider := NewRetryProvider(func(string) assistant.Provider { return partial }, nil, testPolicy())

	seen := ""
	_, err := provider.StreamComplete(context.Background(), nil, nil, func(delta assistant.StreamDelta) error {
		seen += delta.Content
		return nil
	})
	if err == nil {
		t.Fatal("expected the stream failure to surface")
	}
	if seen != "част" {
		t.Fatalf("partial text must be preserved: %q", seen)
	}
	if partial.calls != 1 {
		t.Fatalf("a stream that already emitted text must not restart, got %d calls", partial.calls)
	}
}

type partialFailProvider struct{ calls int }

func (p *partialFailProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	return assistant.Completion{}, errors.New("not implemented")
}

func (p *partialFailProvider) StreamComplete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	p.calls++
	if onDelta != nil {
		if err := onDelta(assistant.StreamDelta{Content: "част"}); err != nil {
			return assistant.Completion{}, err
		}
	}
	return assistant.Completion{}, io.ErrUnexpectedEOF
}

func TestRetryProviderTreatsEmptyAnswerAsRetryable(t *testing.T) {
	emptyFirst := &emptyThenAnswerProvider{}
	provider := NewRetryProvider(func(string) assistant.Provider { return emptyFirst }, nil, testPolicy())

	completion, err := provider.StreamComplete(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("empty answer must be retried: %v", err)
	}
	if completion.Content != "ответ" {
		t.Fatalf("unexpected completion: %+v", completion)
	}
	if emptyFirst.calls != 2 {
		t.Fatalf("expected one retry after an empty answer, got %d", emptyFirst.calls)
	}
}

type emptyThenAnswerProvider struct{ calls int }

func (p *emptyThenAnswerProvider) Complete(context.Context, []assistant.ChatMessage, []assistant.ToolSpec) (assistant.Completion, error) {
	p.calls++
	if p.calls == 1 {
		return assistant.Completion{}, nil
	}
	return assistant.Completion{Content: "ответ"}, nil
}

func (p *emptyThenAnswerProvider) StreamComplete(_ context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, _ func(assistant.StreamDelta) error) (assistant.Completion, error) {
	p.calls++
	if p.calls == 1 {
		return assistant.Completion{}, nil
	}
	return assistant.Completion{Content: "ответ"}, nil
}

func TestRetryableErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"rate limit", &ProviderError{Status: 429}, true},
		{"server error", &ProviderError{Status: 503}, true},
		{"bad request", &ProviderError{Status: 400}, false},
		{"unauthorized", &ProviderError{Status: 401}, false},
		{"transient stream error", &ProviderError{Body: "Provider returned error: temporarily rate-limited", Transient: true}, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"plain error", errors.New("boom"), false},
		{"canceled", context.Canceled, false},
	}
	for _, test := range cases {
		if got := RetryableError(test.err); got != test.want {
			t.Fatalf("%s: RetryableError = %v, want %v", test.name, got, test.want)
		}
	}
}
