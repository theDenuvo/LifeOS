package httptransport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestEventsResyncCursorSkipsEventsItAccountsFor is the review's race: events
// published around the moment the handler resolves a stale position end up in
// the subscriber channel, and the resync cursor already accounts for them.
// After the resync, every delivered event must have an id above that cursor —
// otherwise the browser's Last-Event-ID would move backwards and the next
// reconnect would resync again for nothing.
//
// The handler subscribes first and reads the head right after the headers are
// flushed, so the seam between Subscribe and the head read is narrow: this test
// hammers it by publishing the moment the response headers arrive. The
// deterministic halves of the same contract are pinned by
// TestResolveStalePositionAnchorsTheLiveCursor and
// TestLiveEventDropsEventsTheCursorAlreadyCovers; this test checks that the
// wiring composes them correctly end to end.
func TestEventsResyncCursorSkipsEventsItAccountsFor(t *testing.T) {
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

	for attempt := 0; attempt < 25; attempt++ {
		first, second, third := attempt*3+1, attempt*3+2, attempt*3+3
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		// The client holds ids from a foreign epoch.
		request.Header.Set("Last-Event-ID", "7.9999")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("attempt %d: events request: %v", attempt, err)
		}
		reader := bufio.NewReader(response.Body)

		// The response headers have arrived, which means the server has already
		// subscribed: the resync cursor is on the wire. Publish immediately —
		// the closest an outside observer can get to the seam.
		broker.Publish("task.updated", map[string]any{"n": first})

		head := readUntil(t, reader, "missed_after", 2*time.Second)
		if !strings.Contains(head, "event: resync") {
			t.Fatalf("attempt %d: no resync: %s", attempt, head)
		}
		if strings.Count(head, "event: resync") != 1 {
			t.Fatalf("attempt %d: more than one resync frame: %s", attempt, head)
		}
		cursor := firstIDString(head)
		_, headSeq := parseEventID(cursor)

		// Publish two more events after the resync was read: they must arrive.
		broker.Publish("task.updated", map[string]any{"n": second})
		broker.Publish("task.updated", map[string]any{"n": third})
		// Read by payload, so each frame is read complete: a marker on the
		// event line would stop before the frame's data line.
		tail := readUntil(t, reader, "\"n\":"+strconv.Itoa(second), 2*time.Second)
		tail += readUntil(t, reader, "\"n\":"+strconv.Itoa(third), 2*time.Second)

		if strings.Contains(tail, "event: resync") {
			t.Fatalf("attempt %d: a second resync was sent: %s", attempt, tail)
		}
		ids := sseSeqs(tail)
		// Every delivered id is above the announced cursor, contiguous and
		// without duplicates. The seam event (first) either landed before the
		// head read — the cursor covers it and it must not appear — or after it
		// and leads the delivery; the other two always follow.
		for i, id := range ids {
			if id <= headSeq {
				t.Fatalf("attempt %d: event %d arrived after a resync whose cursor is %d", attempt, id, headSeq)
			}
			if i > 0 && id != ids[i-1]+1 {
				t.Fatalf("attempt %d: ids %v are not contiguous", attempt, ids)
			}
		}
		// Payload check: the events published after the resync are delivered
		// exactly once, and nothing outside this attempt's three events
		// appears.
		payloads := payloadsIn(tail)
		if len(payloads) < 2 || len(payloads) > 3 {
			t.Fatalf("attempt %d: payloads %v, want the two post-resync events and optionally the seam event", attempt, payloads)
		}
		allowed := map[int]bool{first: true, second: true, third: true}
		seen := map[int]bool{}
		for _, n := range payloads {
			if !allowed[n] {
				t.Fatalf("attempt %d: unexpected payload %d", attempt, n)
			}
			if seen[n] {
				t.Fatalf("attempt %d: payload %d delivered twice", attempt, n)
			}
			seen[n] = true
		}
		if !seen[second] || !seen[third] {
			t.Fatalf("attempt %d: the two events published after the resync were not delivered: %v", attempt, payloads)
		}
		response.Body.Close()
	}
}

// payloadsIn extracts the "n" field of every task.updated frame in body.
func payloadsIn(body string) []int {
	var out []int
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[len("data: "):])), &payload); err == nil {
			out = append(out, payload.N)
		}
	}
	return out
}

// TestBrokerHeadMatchesEpochAndID pins the atomic head read: the pair returned
// by Head is the same the broker published, never a mix of two ranges.
func TestBrokerHeadMatchesEpochAndID(t *testing.T) {
	const block = 2
	// rotate makes every reservation carry a fresh epoch, like the durable
	// allocator does.
	reserve := &fixedReserve{epoch: 42, next: 1, block: block, rotate: true}
	broker, err := events.NewBrokerReserved(8, block, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	if err := broker.Publish("a", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	epoch, id := broker.Head()
	if epoch != 42 || id != 1 {
		t.Fatalf("Head = %d,%d, want 42,1", epoch, id)
	}
	// Crossing into the next range changes both values together.
	if err := broker.Publish("b", nil); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if err := broker.Publish("c", nil); err != nil {
		t.Fatalf("publish 3: %v", err)
	}
	epoch, id = broker.Head()
	if epoch == 42 {
		t.Fatalf("epoch did not change after a range renewal: %d", epoch)
	}
	if id != 3 {
		t.Fatalf("Head id = %d, want 3", id)
	}
}

// TestResolveStalePositionAnchorsTheLiveCursor pins the deterministic half of
// the resync-cursor contract: the function returns exactly the cursor it
// announced, so the caller can anchor its live loop to it without a race.
func TestResolveStalePositionAnchorsTheLiveCursor(t *testing.T) {
	reserve := &fixedReserve{epoch: 42, next: 1, block: 1 << 20}
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, 1<<20, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := broker.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// A stale position: the resync announces the head, and the returned cursor
	// is that same head — the value the live loop must anchor to.
	recorder := httptest.NewRecorder()
	sent, err := resolveReconnect(recorder, recorder, broker, "7.9999")
	if err != nil {
		t.Fatalf("resolveReconnect: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "id: 42.2") || !strings.Contains(body, "event: resync") {
		t.Fatalf("resync frame = %q, want cursor 42.2", body)
	}
	if sent != 2 {
		t.Fatalf("resolveReconnect returned %d, want the announced cursor 2", sent)
	}

	// A matching position replays and returns the head as well.
	recorder = httptest.NewRecorder()
	sent, err = resolveReconnect(recorder, recorder, broker, "42.1")
	if err != nil {
		t.Fatalf("resolveReconnect matching: %v", err)
	}
	if strings.Contains(recorder.Body.String(), "event: resync") {
		t.Fatalf("matching epoch triggered a resync: %s", recorder.Body.String())
	}
	if sent != 2 {
		t.Fatalf("resolveReconnect matching returned %d, want the head 2", sent)
	}
}

// TestLiveEventDropsEventsTheCursorAlreadyCovers pins the deterministic half of
// the live-loop contract: whatever the announced cursor already covers is never
// delivered, and whatever is above it is delivered exactly once.
func TestLiveEventDropsEventsTheCursorAlreadyCovers(t *testing.T) {
	reserve := &fixedReserve{epoch: 42, next: 1, block: 1 << 20}
	broker, err := events.NewBrokerReserved(events.DefaultReplaySize, 1<<20, reserve.reserve)
	if err != nil {
		t.Fatalf("build broker: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := broker.Publish("task.updated", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	lastSent := uint64(2) // the resync announced 42.2

	// An event that landed in the channel during the seam between Subscribe
	// and the head read: its id is inside the cursor, it must not be sent.
	stale := events.Event{Epoch: 42, ID: 2, Type: "task.updated", Data: map[string]any{"i": 1}}
	if err := writeLiveEvent(recorder, recorder, broker, stale, &lastSent); err != nil {
		t.Fatalf("stale event: %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("a covered event was delivered: %q", recorder.Body.String())
	}
	if lastSent != 2 {
		t.Fatalf("lastSent moved to %d on a covered event", lastSent)
	}

	// An event above the cursor is delivered and moves the cursor.
	fresh := events.Event{Epoch: 42, ID: 3, Type: "task.updated", Data: map[string]any{"i": 2}}
	if err := writeLiveEvent(recorder, recorder, broker, fresh, &lastSent); err != nil {
		t.Fatalf("fresh event: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "id: 42.3") {
		t.Fatalf("the event above the cursor was not delivered: %q", recorder.Body.String())
	}
	if lastSent != 3 {
		t.Fatalf("lastSent = %d, want 3", lastSent)
	}

	// A burst drop is repaired from the ring: the cursor jumps over the gap and
	// the skipped event is not delivered twice.
	recorder = httptest.NewRecorder()
	dropped := events.Event{Epoch: 42, ID: 5, Type: "task.updated", Data: map[string]any{"i": 4}}
	if err := writeLiveEvent(recorder, recorder, broker, dropped, &lastSent); err != nil {
		t.Fatalf("gap event: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "id: 42.4") || !strings.Contains(body, "id: 42.5") {
		t.Fatalf("the gap was not closed from the ring: %q", body)
	}
	if lastSent != 5 {
		t.Fatalf("lastSent = %d after the gap repair, want 5", lastSent)
	}
}
