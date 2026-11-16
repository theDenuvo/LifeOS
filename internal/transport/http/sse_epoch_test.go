package httptransport

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// fixedReserve hands out sequential blocks under one epoch, or a fresh epoch per
// call, depending on the test.
type fixedReserve struct {
	epoch       uint64
	next        uint64
	block       uint64
	rotate      bool
	rotateEvery int
	calls       int
}

func (r *fixedReserve) reserve(ctx context.Context, block uint64) (uint64, uint64, error) {
	r.calls++
	epoch := r.epoch
	if r.rotate {
		if r.calls > 1 {
			epoch = r.epoch + uint64(r.calls-1)
		}
	}
	start := r.next
	r.next += block
	return epoch, start, nil
}

// TestEventsEpochMismatchOrdersFullResync covers the rollback case the review
// named: the browser holds ids from a database that was replaced by an older
// copy, so the numbers alone look plausible but skip a lost window. A different
// epoch must order a full resynchronisation instead of a partial replay.
func TestEventsEpochMismatchOrdersFullResync(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	// The process runs with epoch 42. The browser, however, answers with ids
	// from epoch 7 — an older, restored database.
	reserve := &fixedReserve{epoch: 42, next: 1, block: 1 << 20}
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, 1<<20, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	handler.services.Broker = broker

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Last-Event-ID", "7.5000")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	head := readUntil(t, reader, "missed_after", 3*time.Second)
	if !strings.Contains(head, "event: resync") {
		t.Fatalf("an id from another epoch was accepted: %s", head)
	}
	// The frame hands the client a cursor in the current epoch, so the next
	// reconnect is provable again.
	if got := firstIDString(head); got != "42.0" {
		t.Fatalf("resync cursor = %q, want 42.0 (the head is empty)", got)
	}

	// A matching epoch replays normally.
	broker.Publish("task.updated", map[string]any{"i": 1})
	line := readUntil(t, reader, "event: task.updated", 3*time.Second)
	if !strings.Contains(line, "id: 42.1") {
		t.Fatalf("event id = %q, want 42.1", line)
	}
}

// TestEventsPlainNumericIDIsNeverTrusted pins the provenance rule: an id without
// an epoch cannot prove it belongs to this database, so it always gets a resync.
// This is what protects a client that was pointed at a database restored from a
// copy old enough to have no epoch at all.
func TestEventsPlainNumericIDIsNeverTrusted(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	reserve := &fixedReserve{epoch: 42, next: 1, block: 1 << 20}
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, 1<<20, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	handler.services.Broker = broker

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// A plain number: either a very old client or a manipulated cursor.
	request.Header.Set("Last-Event-ID", "5000")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	head := readUntil(t, bufio.NewReader(response.Body), "missed_after", 3*time.Second)
	if !strings.Contains(head, "event: resync") {
		t.Fatalf("a numeric id was replayed against the current database: %s", head)
	}
}

// TestEventsMatchingEpochReplays covers the positive path: same epoch, older
// seq, and the buffered events arrive in order without a resync.
func TestEventsMatchingEpochReplays(t *testing.T) {
	handler, cleanup := setupTestHandler(t)
	defer cleanup()

	reserve := &fixedReserve{epoch: 42, next: 1, block: 1 << 20}
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, 1<<20, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	handler.services.Broker = broker

	for i := 0; i < 5; i++ {
		if err := broker.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Last-Event-ID", "42.2")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer response.Body.Close()

	body := readUntil(t, bufio.NewReader(response.Body), "id: 42.5", 3*time.Second)
	if strings.Contains(body, "event: resync") {
		t.Fatalf("matching epoch triggered a resync: %s", body)
	}
	if got := sseSeqs(body); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("replayed seqs = %v, want 3,4,5", got)
	}
}

// TestParseEventIDFormats pins the id wire format the epoch contract relies on.
func TestParseEventIDFormats(t *testing.T) {
	if got := formatEventID(42, 7); got != "42.7" {
		t.Fatalf("formatEventID(42, 7) = %q, want 42.7", got)
	}
	if got := formatEventID(0, 7); got != "7" {
		t.Fatalf("formatEventID(0, 7) = %q, want 7", got)
	}
	epoch, seq := parseEventID("42.7")
	if epoch != 42 || seq != 7 {
		t.Fatalf("parseEventID(42.7) = %d,%d", epoch, seq)
	}
	epoch, seq = parseEventID("7")
	if epoch != 0 || seq != 7 {
		t.Fatalf("parseEventID(7) = %d,%d, want 0,7", epoch, seq)
	}
	// A malformed id maps to epoch 0, which can never match a production epoch.
	epoch, seq = parseEventID("not-a-number")
	if epoch != 0 {
		t.Fatalf("parseEventID(malformed) = %d,%d, want epoch 0", epoch, seq)
	}
}

// firstIDString returns the raw id of the first frame in body.
func firstIDString(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "id: ") {
			return strings.TrimSpace(line[len("id: "):])
		}
	}
	return ""
}

// sseIDs extracts the seq part of the ids in a stream body.
func sseSeqs(body string) []uint64 {
	var out []uint64
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id: ") {
			continue
		}
		_, seq := parseEventID(strings.TrimSpace(line[len("id: "):]))
		out = append(out, seq)
	}
	return out
}
