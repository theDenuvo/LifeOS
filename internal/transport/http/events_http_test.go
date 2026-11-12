package httptransport

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readSSE reads one complete SSE frame (lines up to the blank separator).
func readSSE(t *testing.T, reader *bufio.Reader) map[string]string {
	t.Helper()
	frame := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(frame) == 0 {
				continue
			}
			return frame
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		frame[key] = value
	}
}

func TestSSEStreamDeliversTaskEvents(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("content type = %q", contentType)
	}

	reader := bufio.NewReader(response.Body)

	// The handler subscribes before writing headers, so a publish after the
	// response is received cannot be missed.
	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Событие"}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	frame := readSSE(t, reader)
	if frame["event"] != "task.created" {
		t.Errorf("event = %q, want task.created", frame["event"])
	}
	if frame["id"] == "" || !strings.Contains(frame["data"], `"id"`) {
		t.Errorf("frame = %v", frame)
	}
}

func TestSSEReplaysAfterLastEventID(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	broker := handler.services.Events
	broker.Publish("task.created", map[string]any{"id": 1})
	broker.Publish("task.updated", map[string]any{"id": 1})

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer response.Body.Close()

	frame := readSSE(t, bufio.NewReader(response.Body))
	if frame["id"] != "2" || frame["event"] != "task.updated" {
		t.Errorf("replayed frame = %v, want id 2 / task.updated", frame)
	}
}

func TestSSEInboxEvents(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	handler, cleanup := newTaskAPI(t, now)
	defer cleanup()

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	recorder, _ := apiRequest(t, handler, http.MethodPost, "/api/v1/inbox", map[string]any{"text": "Мысль"}, nil)
	requireStatus(t, recorder, http.StatusCreated)

	frame := readSSE(t, reader)
	if frame["event"] != "inbox.captured" {
		t.Errorf("event = %q, want inbox.captured", frame["event"])
	}
	if !strings.Contains(frame["data"], `"id"`) {
		t.Errorf("data = %q", frame["data"])
	}
}
