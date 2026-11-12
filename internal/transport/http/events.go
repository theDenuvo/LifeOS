package httptransport

import (
	"encoding/json"
	"fmt"
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

	broker := h.services.Events
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

// writeSSE renders one event in the text/event-stream wire format.
func writeSSE(w http.ResponseWriter, event events.Event) error {
	payload, err := json.Marshal(event.Data)
	if err != nil {
		payload = []byte("{}")
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
	return err
}
