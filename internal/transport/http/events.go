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
			for _, event := range broker.Replay(parsed) {
				if err := writeSSE(w, event); err != nil {
					return
				}
				lastSent = event.ID
			}
			flusher.Flush()
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
			if event.ID <= lastSent {
				continue
			}
			// A subscriber channel is deliberately non-blocking, so a burst can
			// drop an event. The replay buffer usually still holds it: when the
			// next delivered event skips an id, send the missing ones from the
			// ring instead of letting a gap reach the browser.
			if lastSent > 0 && event.ID > lastSent+1 {
				from := lastSent
				replayed := broker.Replay(lastSent)
				// The ring is bounded, so a long enough gap can outrun it. When
				// the oldest buffered event is already past the gap, some events
				// are genuinely gone and replaying the rest would leave the
				// client silently inconsistent. Tell it to re-read its state
				// instead of pretending the stream is complete.
				if closesGap(replayed, lastSent) {
					for _, missed := range replayed {
						if missed.ID >= event.ID {
							break
						}
						if err := writeSSE(w, missed); err != nil {
							return
						}
						lastSent = missed.ID
					}
				} else {
					if err := writeResync(w, from, event.ID, len(replayed)); err != nil {
						return
					}
					lastSent = event.ID - 1
				}
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

// writeResync tells the client that events between from and to are no longer
// recoverable, so pages reload instead of rendering a state built on a stream
// with a silent hole.
func writeResync(w http.ResponseWriter, from, to uint64, recovered int) error {
	payload, _ := json.Marshal(map[string]any{
		"reason": "gap", "missed_after": from, "until": to, "recovered": recovered,
	})
	slog.Warn("events stream gap exceeded the replay buffer", "after", from, "until", to, "recovered", recovered)
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", resyncType, payload)
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
