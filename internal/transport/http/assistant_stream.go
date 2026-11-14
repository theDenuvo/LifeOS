package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/theDenuvo/LifeOS/internal/agent"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

const (
	settingAssistantModel       = "assistant.model"
	settingAssistantAutoConfirm = "assistant.auto_confirm"
)

// streamAssistantMessage handles POST /api/v1/assistant/messages/stream.
func (h *Handler) streamAssistantMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content        string  `json:"content"`
		ConversationID int64   `json:"conversation_id"`
		Model          string  `json:"model"`
		AutoConfirm    bool    `json:"auto_confirm"`
		DocumentIDs    []int64 `json:"document_ids"`
		Interaction    struct {
			Screen     string `json:"screen"`
			ObjectType string `json:"object_type"`
			ObjectID   string `json:"object_id"`
			Title      string `json:"title"`
		} `json:"interaction"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	if err := h.validateAssistantModel(r, body.Model); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	documents, err := h.loadDocumentRefs(r, body.DocumentIDs)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "streaming is not supported")
		return
	}
	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream; charset=utf-8")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	headers.Set("X-Accel-Buffering", "no")
	// This response legitimately outlives Server.WriteTimeout: a model may think
	// for minutes before the first token. Clearing the deadline must not be
	// swallowed, otherwise the stream dies mid-turn.
	if err := clearWriteDeadline(w); err != nil {
		slog.Warn("assistant stream: write deadline is not cleared; long turns may be cut", "error", err)
	}

	stream := &sseWriter{writer: w, flusher: flusher}
	heartbeat := stream.startHeartbeat(r.Context())
	defer heartbeat()

	emit := func(event assistant.StreamEvent) error {
		data := event.Data
		switch event.Type {
		case "done":
			result, ok := data.(assistant.SendResult)
			if !ok {
				return nil
			}
			pending := make([]pendingActionBody, 0, len(result.PendingActions))
			for _, action := range result.PendingActions {
				pending = append(pending, newPendingActionBody(action))
			}
			data = map[string]any{
				"conversation_id": result.ConversationID,
				"message":         newAssistantMessageBody(result.Message),
				"pending_actions": pending,
			}
		case "pending_action":
			if action, ok := data.(*domainassistant.PendingAction); ok {
				data = newPendingActionBody(action)
			}
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return stream.event(event.Type, payload)
	}

	result, err := h.services.Assistant.SendMessageStream(r.Context(), body.ConversationID, body.Content, assistant.InteractionContext{
		Screen:     body.Interaction.Screen,
		ObjectType: body.Interaction.ObjectType,
		ObjectID:   body.Interaction.ObjectID,
		Title:      body.Interaction.Title,
		Documents:  documents,
	}, body.Model, body.AutoConfirm, emit)
	if err != nil {
		if r.Context().Err() == nil {
			_ = emit(assistant.StreamEvent{Type: "error", Data: map[string]any{"message": assistant.FailureMessage(err)}})
		}
		return
	}
	_ = result
}

// clearWriteDeadline removes the server's write timeout for one response.
func clearWriteDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

// sseWriter serialises SSE frames written from the handler and the heartbeat
// goroutine, so keepalives can never interleave inside a data frame.
type sseWriter struct {
	mu      sync.Mutex
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) event(name string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", name, payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseWriter) ping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.writer, ": keepalive\n\n"); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// startHeartbeat keeps the connection alive while the model is silent (long
// reasoning, tool execution). It returns a stop function.
func (s *sseWriter) startHeartbeat(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.ping(); err != nil {
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func (h *Handler) loadDocumentRefs(r *http.Request, ids []int64) ([]assistant.DocumentRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if h.services.Documents == nil {
		return nil, fmt.Errorf("documents are not available")
	}
	refs := make([]assistant.DocumentRef, 0, len(ids))
	for _, id := range ids {
		view, err := h.services.Documents.Get(r.Context(), id)
		if err != nil {
			return nil, fmt.Errorf("документ #%d: %w", id, err)
		}
		if string(view.Document.Status) != "processed" {
			return nil, fmt.Errorf("документ «%s» ещё не сконвертирован", view.Document.Filename)
		}
		chars := 0
		if view.Document.ExtractedText != nil {
			chars = utf8.RuneCountInString(*view.Document.ExtractedText)
		}
		refs = append(refs, assistant.DocumentRef{
			ID: view.Document.ID, Filename: view.Document.Filename, Size: view.Document.Size, MarkdownChars: chars,
		})
	}
	return refs, nil
}

func (h *Handler) validateAssistantModel(r *http.Request, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	if h.services.Models != nil {
		return h.services.Models.ValidateFree(r.Context(), model)
	}
	if strings.Contains(strings.ToLower(h.cfg.LLMBaseURL), "openrouter.ai") && !strings.HasSuffix(model, ":free") {
		return fmt.Errorf("в OpenRouter разрешены только модели с суффиксом :free")
	}
	return nil
}

// getAssistantConfig handles GET /api/v1/assistant/config.
func (h *Handler) getAssistantConfig(w http.ResponseWriter, r *http.Request) {
	model := h.cfg.LLMModel
	if raw, err := h.services.Settings.Get(r.Context(), settingAssistantModel); err == nil {
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			model = value
		}
	}
	autoConfirm := false
	if raw, err := h.services.Settings.Get(r.Context(), settingAssistantAutoConfirm); err == nil {
		_ = json.Unmarshal(raw, &autoConfirm)
	}
	models := []agent.ModelInfo{}
	if h.services.Models != nil {
		models = h.services.Models.List(r.Context())
	}
	if len(models) == 0 && model != "" {
		models = []agent.ModelInfo{{ID: model, Name: model}}
	}
	if strings.Contains(strings.ToLower(h.cfg.LLMBaseURL), "openrouter.ai") && !strings.HasSuffix(model, ":free") && len(models) > 0 {
		model = models[0].ID
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"models":       models,
		"model":        model,
		"auto_confirm": autoConfirm,
	})
}

// putAssistantConfig handles PUT /api/v1/assistant/config.
func (h *Handler) putAssistantConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model       *string `json:"model"`
		AutoConfirm *bool   `json:"auto_confirm"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	if body.Model != nil {
		if err := h.validateAssistantModel(r, *body.Model); err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		}
		value, _ := json.Marshal(*body.Model)
		if err := h.services.Settings.Put(r.Context(), settingAssistantModel, value); err != nil {
			h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
			return
		}
	}
	if body.AutoConfirm != nil {
		value, _ := json.Marshal(*body.AutoConfirm)
		if err := h.services.Settings.Put(r.Context(), settingAssistantAutoConfirm, value); err != nil {
			h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
			return
		}
	}
	h.getAssistantConfig(w, r)
}
