package httptransport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// slowProvider answers only after a delay, like a model that thinks for a while
// before the first token.
type slowProvider struct {
	delay time.Duration
	text  string
}

func (p slowProvider) Complete(ctx context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec) (assistant.Completion, error) {
	return p.StreamComplete(ctx, nil, nil, nil)
}

func (p slowProvider) StreamComplete(ctx context.Context, _ []assistant.ChatMessage, _ []assistant.ToolSpec, onDelta func(assistant.StreamDelta) error) (assistant.Completion, error) {
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return assistant.Completion{}, ctx.Err()
	case <-timer.C:
	}
	if onDelta != nil {
		if err := onDelta(assistant.StreamDelta{Content: p.text}); err != nil {
			return assistant.Completion{}, err
		}
	}
	return assistant.Completion{Content: p.text}, nil
}

// The assistant stream must outlive http.Server.WriteTimeout. A long model turn
// used to be cut at exactly 30 s (or, in this test, at 1 s) because the logging
// middleware hid the writer from http.ResponseController, so the write deadline
// was never cleared.
func TestAssistantStreamOutlivesWriteTimeout(t *testing.T) {
	handler, cleanup := setupTestHandlerWithProvider(t, slowProvider{delay: 1500 * time.Millisecond, text: "готово"})
	defer cleanup()

	server := httptest.NewUnstartedServer(handler.Routes())
	server.Config.WriteTimeout = 1 * time.Second
	server.Start()
	defer server.Close()

	body := strings.NewReader(`{"content":"создай 40 событий"}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/assistant/messages/stream", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}

	var done map[string]any
	var streamError string
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			switch event {
			case "done":
				if err := json.Unmarshal([]byte(payload), &done); err != nil {
					t.Fatalf("decode done: %v", err)
				}
			case "error":
				var problem struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal([]byte(payload), &problem)
				streamError = problem.Message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("stream was cut before completion: %v", err)
	}
	if streamError != "" {
		t.Fatalf("stream reported an error: %s", streamError)
	}
	if done == nil {
		t.Fatal("stream ended without a done event")
	}
	message, _ := done["message"].(map[string]any)
	if content, _ := message["content"].(string); content != "готово" {
		t.Fatalf("unexpected answer: %+v", message)
	}
}

// The live event stream is long-lived by design and must not be closed by the
// server write timeout either.
func TestEventsStreamOutlivesWriteTimeout(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	server := httptest.NewUnstartedServer(handler.Routes())
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	// Keep the connection open past the write timeout, then confirm it still
	// accepts a write by publishing through the broker.
	time.Sleep(400 * time.Millisecond)
	handler.services.Broker.Publish("task.created", map[string]any{"id": 1})

	reader := bufio.NewReader(response.Body)
	deadline := time.Now().Add(3 * time.Second)
	line := ""
	for time.Now().Before(deadline) {
		read, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("live stream was closed after the write timeout: %v", err)
		}
		if strings.Contains(read, "task.created") {
			line = read
			break
		}
	}
	if line == "" {
		t.Fatal("event did not arrive on the live stream")
	}
}

// TestClosesGapDistinguishesRecoverableFromLost covers the limitation the
// review named: the replay ring is bounded, so a long enough gap cannot be
// repaired by replay. The stream must be able to tell the difference.
func TestClosesGapDistinguishesRecoverableFromLost(t *testing.T) {
	mk := func(ids ...uint64) []events.Event {
		out := make([]events.Event, 0, len(ids))
		for _, id := range ids {
			out = append(out, events.Event{ID: id, Type: "task.updated"})
		}
		return out
	}

	cases := []struct {
		name     string
		replayed []events.Event
		lastSent uint64
		want     bool
	}{
		{"empty ring cannot help", mk(), 10, false},
		{"contiguous replay closes the gap", mk(11, 12, 13), 10, true},
		{"oldest buffered entry is still too new", mk(45, 46), 10, false},
		{"single next event is enough", mk(11), 10, true},
	}
	for _, tc := range cases {
		if got := closesGap(tc.replayed, tc.lastSent); got != tc.want {
			t.Fatalf("%s: closesGap = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestWriteResyncReportsTheRange pins the wire format the client parses when it
// decides to re-read its state.
func TestWriteResyncReportsTheRange(t *testing.T) {
	recorder := httptest.NewRecorder()
	if err := writeResync(recorder, 10, 400, 256); err != nil {
		t.Fatalf("writeResync: %v", err)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "event: resync") {
		t.Fatalf("resync event type missing: %q", body)
	}
	if !strings.Contains(body, "\"reason\":\"gap\"") || !strings.Contains(body, "\"until\":400") {
		t.Fatalf("resync payload = %q", body)
	}
}
