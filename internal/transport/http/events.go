package httptransport

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/theDenuvo/LifeOS/internal/events"
)

// streamEvents handles GET /api/v1/events.
//
// The stream is one-way (server → client), which is exactly what SSE is for:
// client actions keep going through REST. A reconnecting client sends
// Last-Event-ID and receives the buffered events it missed.
func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Streaming is not supported")
		return
	}

	broker := h.services.Broker
	if broker == nil {
		// Without a broker the stream stays open but silent; this only happens
		// in tests that construct a partial Services value.
		<-r.Context().Done()
		return
	}

	// Subscribe before writing headers and before replaying, so a client that
	// has received the response can never miss an event published in between.
	stream := broker.Subscribe(r.Context())
	lastSent := uint64(0)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// The live stream is open for the whole session; Server.WriteTimeout must not
	// cut it every 30 seconds.
	if err := clearWriteDeadline(w); err != nil {
		slog.Warn("events stream: write deadline is not cleared", "error", err)
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			lastSent, err = catchUp(w, broker, flusher, parsed)
			if err != nil {
				return
			}
		}
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-stream:
			if !ok {
				return
			}
			// A subscriber channel is deliberately non-blocking, so a burst can
			// drop an event. When the next delivered event skips an id, catch up
			// from the ring before continuing.
			if lastSent > 0 && event.ID > lastSent+1 {
				caughtUp, err := catchUp(w, broker, flusher, lastSent)
				if err != nil {
					return
				}
				lastSent = caughtUp
			}
			// catchUp already sent this event from the buffer when it could.
			if event.ID <= lastSent {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			lastSent = event.ID
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// resyncType is a client signal, not a domain event to store: it says that the
// stream could not be repaired and the client must re-read the current state.
const resyncType = "resync"

// catchUp sends everything the client is missing after its position and returns
// the newest id it now has.
//
// The ring covers short absences. When it cannot, the client gets a resync frame
// that both says "re-read your state" and advances the EventSource cursor, so a
// second reconnect does not report the same gap again. An empty replay is not a
// gap by itself: it also means the client is already at the head, or that a
// restarted process has published nothing yet.
func catchUp(w http.ResponseWriter, broker *events.Broker, flusher http.Flusher, after uint64) (uint64, error) {
	// Every branch below writes something the client must see immediately: a
	// resync that sits in the buffer until the next heartbeat would leave the
	// interface stale for seconds.
	defer flusher.Flush()

	lastID := broker.LastID()
	replayed := broker.Replay(after)

	switch {
	case after == 0:
		// The client says it has nothing, so the buffer is a bonus, not a gap.
	case len(replayed) == 0 && after == lastID:
		// The client is exactly at the head: it missed nothing.
		return after, nil
	case len(replayed) == 0:
		// Either the client is behind the head with an empty buffer, or it is
		// ahead of it. Both need a resync; the second only happens when the
		// durable id range was lost with the database, and rebasing the cursor
		// is what stops every new event from looking older than the client.
		if err := writeResync(w, lastID, after, lastID, 0); err != nil {
			return after, err
		}
		return lastID, nil
	case !closesGap(replayed, after):
		// The oldest buffered event is already past the client's position, so
		// replay cannot start where it stopped. Announce what is lost, then send
		// what survived.
		oldest := replayed[0].ID
		if err := writeResync(w, oldest-1, after, oldest-1, len(replayed)); err != nil {
			return after, err
		}
	}

	sent := after
	for _, event := range replayed {
		if err := writeSSE(w, event); err != nil {
			return sent, err
		}
		sent = event.ID
	}
	return sent, nil
}

// closesGap reports whether the buffered events can cover a gap that starts
// right after lastSent.
//
// The ring keeps the newest N events, so the oldest entry is the earliest one
// replay can start from. If that entry is already later than lastSent+1, the
// ids in between were evicted and are unrecoverable.
func closesGap(replayed []events.Event, lastSent uint64) bool {
	if len(replayed) == 0 {
		return false
	}
	return replayed[0].ID <= lastSent+1
}

// writeResync tells the client that events after `after` up to `until` are not
// recoverable, so pages reload instead of rendering a state built on a stream
// with a silent hole.
//
// The frame carries an id on purpose: it advances the browser's Last-Event-ID to
// cursor, so reconnecting after a resync asks for the right range instead of
// reporting the same gap forever.
func writeResync(w http.ResponseWriter, cursor, after, until uint64, recovered int) error {
	payload, _ := json.Marshal(map[string]any{
		"reason": "gap", "missed_after": after, "until": until, "recovered": recovered,
	})
	slog.Warn("events stream gap exceeded the replay buffer", "after", after, "until", until, "recovered", recovered)
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", cursor, resyncType, payload)
	return err
}

// writeSSE renders one event in the text/event-stream wire format.
func writeSSE(w http.ResponseWriter, event events.Event) error {
	payload, err := json.Marshal(event.Data)
	if err != nil {
		payload = []byte("{}")
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
	return err
}
