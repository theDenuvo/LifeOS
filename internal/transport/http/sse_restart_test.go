package httptransport

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// restartSequence is the start of the id range a restarted process reserves. It
// is far above anything the previous run produced, exactly like a durable
// counter that was advanced by a block.
const restartSequence = 4097

// TestEventsSurviveRestartWithStaleLastEventID reproduces the review scenario:
// a client saw id 500, LifeOS restarted and its clock starts again at a fresh
// value, and the reconnecting client sends Last-Event-ID 500. Before the fix the
// server treated the empty replay as a gap, reset its cursor to 500 and then
// dropped every new event as "older than the client".
func TestEventsSurviveRestartWithStaleLastEventID(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// The first process publishes up to id 500.
	oldBroker := handler.services.Broker
	for i := 0; i < 500; i++ {
		oldBroker.Publish("task.updated", map[string]any{"i": i})
	}
	if last := oldBroker.LastID(); last != 500 {
		t.Fatalf("first process last id = %d, want 500", last)
	}

	// The process restarts: a new broker with an empty ring, whose ids continue
	// above the durable range the previous run used.
	handler.services.Broker = events.NewBrokerAt(restartSequence, events.DefaultReplaySize)
	broker := handler.services.Broker

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Last-Event-ID", "500")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	// The restart really did lose the events the client asked for, so it is told
	// once — and the frame advances the cursor to just before the new range.
	head := readUntil(t, reader, "missed_after", 3*time.Second)
	if !strings.Contains(head, "event: resync") {
		t.Fatalf("restart did not announce the lost window: %s", head)
	}
	cursor := lastIDIn(head)
	if cursor != restartSequence-1 {
		t.Fatalf("resync cursor = %d, want %d", cursor, restartSequence-1)
	}

	// The new event must reach the client instead of being dropped as stale.
	broker.Publish("task.updated", map[string]any{"after": "restart"})
	line := readUntil(t, reader, "after", 3*time.Second)
	if !strings.Contains(line, "id: "+strconv.FormatUint(restartSequence, 10)) {
		t.Fatalf("new event %d was dropped after the restart: %s", restartSequence, line)
	}
	if !strings.Contains(line, "event: task.updated") {
		t.Fatalf("new event arrived with the wrong type: %s", line)
	}
}

// TestEventsReconnectAtHeadDoesNotResync covers the second half of the review:
// an empty replay also means "you are up to date", and that must not be reported
// as a lost history.
func TestEventsReconnectAtHeadDoesNotResync(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()
	broker := handler.services.Broker

	for i := 0; i < 3; i++ {
		broker.Publish("task.updated", map[string]any{"i": i})
	}
	headID := broker.LastID()

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Last-Event-ID", strconv.FormatUint(headID, 10))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	broker.Publish("task.updated", map[string]any{"after": "head"})
	body := readUntil(t, reader, "after", 3*time.Second)
	if strings.Contains(body, "event: resync") {
		t.Fatalf("a client that is exactly at the head was told it lost history: %s", body)
	}
	if !strings.Contains(body, "id: "+strconv.FormatUint(headID+1, 10)) {
		t.Fatalf("next event after a head reconnect was not delivered in sequence: %s", body)
	}
}

// TestResyncAdvancesCursorAcrossReconnects checks the property the review asked
// for directly: after a resync, reconnecting with the new Last-Event-ID must not
// ask for the same lost window again.
func TestResyncAdvancesCursorAcrossReconnects(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()
	broker := handler.services.Broker

	total := events.DefaultReplaySize + 40
	for i := 0; i < total; i++ {
		broker.Publish("task.updated", map[string]any{"i": i})
	}
	oldestBuffered := uint64(total - events.DefaultReplaySize + 1)

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	firstRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	firstRequest.Header.Set("Last-Event-ID", "1")
	firstResponse, err := http.DefaultClient.Do(firstRequest)
	if err != nil {
		t.Fatalf("first connect: %v", err)
	}
	first := readUntil(t, bufio.NewReader(firstResponse.Body), "missed_after", 3*time.Second)
	firstResponse.Body.Close()
	if got := lastIDIn(first); got != oldestBuffered-1 {
		t.Fatalf("first resync cursor = %d, want %d", got, oldestBuffered-1)
	}

	// The browser now reconnects with the cursor it learned from the resync.
	secondRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	secondRequest.Header.Set("Last-Event-ID", strconv.FormatUint(oldestBuffered-1, 10))
	secondResponse, err := http.DefaultClient.Do(secondRequest)
	if err != nil {
		t.Fatalf("second connect: %v", err)
	}
	defer secondResponse.Body.Close()

	second := readUntil(t, bufio.NewReader(secondResponse.Body), "id: "+strconv.FormatUint(oldestBuffered, 10), 3*time.Second)
	if strings.Contains(second, "event: resync") {
		t.Fatalf("the same gap was reported twice: %s", second)
	}
}

// lastIDIn returns the id of the first frame in body.
func lastIDIn(body string) uint64 {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id: ") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(line[len("id: "):]), 10, 64)
		if err == nil {
			return id
		}
	}
	return 0
}
