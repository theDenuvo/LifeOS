package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/theDenuvo/LifeOS/internal/agent"
	"github.com/theDenuvo/LifeOS/internal/application/assistant"
	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

type assistantMessageBody struct {
	ID             int64                        `json:"id"`
	ConversationID int64                        `json:"conversation_id"`
	Role           string                       `json:"role"`
	Content        string                       `json:"content"`
	ToolCalls      json.RawMessage              `json:"tool_calls,omitempty"`
	Metadata       json.RawMessage              `json:"metadata,omitempty"`
	Attachments    []domainassistant.Attachment `json:"attachments"`
	Reasoning      string                       `json:"reasoning,omitempty"`
	CreatedAt      string                       `json:"created_at"`
}

type pendingActionBody struct {
	ID             int64           `json:"id"`
	ConversationID int64           `json:"conversation_id"`
	MessageID      int64           `json:"message_id"`
	ToolName       string          `json:"tool_name"`
	Arguments      json.RawMessage `json:"arguments"`
	Preview        json.RawMessage `json:"preview"`
	Status         string          `json:"status"`
	CreatedAt      string          `json:"created_at"`
	ResolvedAt     *string         `json:"resolved_at"`
	Resolution     *string         `json:"resolution"`
}

type conversationBody struct {
	ID                   int64   `json:"id"`
	Title                *string `json:"title"`
	Kind                 string  `json:"kind"`
	Purpose              *string `json:"purpose"`
	SourceConversationID *int64  `json:"source_conversation_id"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

func newConversationBody(conversation *domainassistant.Conversation) conversationBody {
	kind := string(conversation.Kind)
	if kind == "" {
		kind = string(domainassistant.KindMain)
	}
	return conversationBody{
		ID:                   conversation.ID,
		Title:                conversation.Title,
		Kind:                 kind,
		Purpose:              conversation.Purpose,
		SourceConversationID: conversation.SourceConversationID,
		CreatedAt:            formatTime(conversation.CreatedAt),
		UpdatedAt:            formatTime(conversation.UpdatedAt),
	}
}

func newAssistantMessageBody(message *domainassistant.Message) assistantMessageBody {
	attachments := message.Attachments
	if attachments == nil {
		attachments = []domainassistant.Attachment{}
	}
	return assistantMessageBody{
		ID:             message.ID,
		ConversationID: message.ConversationID,
		Role:           string(message.Role),
		Content:        message.Content,
		ToolCalls:      message.ToolCalls,
		Metadata:       message.Metadata,
		Attachments:    attachments,
		Reasoning:      message.Reasoning,
		CreatedAt:      formatTime(message.CreatedAt),
	}
}

func newPendingActionBody(action *domainassistant.PendingAction) pendingActionBody {
	body := pendingActionBody{
		ID:             action.ID,
		ConversationID: action.ConversationID,
		MessageID:      action.MessageID,
		ToolName:       action.ToolName,
		Arguments:      action.Arguments,
		Preview:        action.Preview,
		Status:         string(action.Status),
		CreatedAt:      formatTime(action.CreatedAt),
		Resolution:     action.Resolution,
	}
	if action.ResolvedAt != nil {
		value := formatTime(*action.ResolvedAt)
		body.ResolvedAt = &value
	}
	return body
}

// sendAssistantMessage handles POST /api/v1/assistant/messages.
func (h *Handler) sendAssistantMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content        string  `json:"content"`
		ConversationID int64   `json:"conversation_id"`
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

	documents, err := h.loadDocumentRefs(r, body.DocumentIDs)
	if err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	result, err := h.services.Assistant.SendMessageWithInteraction(r.Context(), body.ConversationID, body.Content, assistant.InteractionContext{
		Screen:     body.Interaction.Screen,
		ObjectType: body.Interaction.ObjectType,
		ObjectID:   body.Interaction.ObjectID,
		Title:      body.Interaction.Title,
		Documents:  documents,
	})
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}

	pending := make([]pendingActionBody, 0, len(result.PendingActions))
	for _, action := range result.PendingActions {
		pending = append(pending, newPendingActionBody(action))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"conversation_id": result.ConversationID,
		"message":         newAssistantMessageBody(result.Message),
		"pending_actions": pending,
	})
}

// listAssistantConversations handles GET /api/v1/assistant/conversations.
func (h *Handler) listAssistantConversations(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	var kind *domainassistant.Kind
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		value := domainassistant.Kind(raw)
		if !value.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid kind; use main or project")
			return
		}
		kind = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid limit")
			return
		}
		limit = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid offset")
			return
		}
		offset = value
	}

	conversations, total, err := h.services.Assistant.ListConversations(r.Context(), limit, offset, kind)
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}

	data := make([]conversationBody, 0, len(conversations))
	for _, conversation := range conversations {
		data = append(data, newConversationBody(conversation))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": limit, "offset": offset, "total": total},
	})
}

// createWorkspace handles POST /api/v1/assistant/workspaces.
func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Purpose string `json:"purpose"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	conversation, err := h.services.Assistant.CreateWorkspace(r.Context(), body.Title, body.Purpose, nil)
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newConversationBody(conversation))
}

// getAssistantConversation handles GET /api/v1/assistant/conversations/{id}.
func (h *Handler) getAssistantConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	view, err := h.services.Assistant.GetConversation(r.Context(), id)
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}

	messages := make([]assistantMessageBody, 0, len(view.Messages))
	for _, message := range view.Messages {
		messages = append(messages, newAssistantMessageBody(message))
	}
	pending := make([]pendingActionBody, 0, len(view.PendingActions))
	for _, action := range view.PendingActions {
		pending = append(pending, newPendingActionBody(action))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"id":                     view.Conversation.ID,
		"title":                  view.Conversation.Title,
		"kind":                   string(view.Conversation.Kind),
		"purpose":                view.Conversation.Purpose,
		"source_conversation_id": view.Conversation.SourceConversationID,
		"created_at":             formatTime(view.Conversation.CreatedAt),
		"updated_at":             formatTime(view.Conversation.UpdatedAt),
		"messages":               messages,
		"pending_actions":        pending,
	})
}

// confirmPendingAction handles POST /api/v1/assistant/pending-actions/{id}/confirm.
func (h *Handler) confirmPendingAction(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	result, err := h.services.Assistant.ConfirmPendingAction(r.Context(), id)
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"pending_action": newPendingActionBody(result.Action),
		"result":         result.Result,
	})
}

// rejectPendingAction handles POST /api/v1/assistant/pending-actions/{id}/reject.
func (h *Handler) rejectPendingAction(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	action, err := h.services.Assistant.RejectPendingAction(r.Context(), id)
	if err != nil {
		h.writeAssistantError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"pending_action": newPendingActionBody(action)})
}

// writeAssistantError maps assistant errors onto problem responses.
func (h *Handler) writeAssistantError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, assistant.ErrConversationNotFound), errors.Is(err, assistant.ErrPendingActionNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", err.Error())
	case errors.Is(err, assistant.ErrPendingActionResolved):
		h.writeProblem(w, r, http.StatusConflict, "conflict", "Conflict", err.Error())
	case errors.Is(err, assistant.ErrProviderNotConfigured):
		h.writeProblem(w, r, http.StatusServiceUnavailable, "provider-not-configured", "Assistant Not Configured",
			"LLM provider is not configured; set LIFEOS_LLM_API_KEY or use a local provider")
	case errors.Is(err, domainassistant.ErrInvalidContent), errors.Is(err, domainassistant.ErrInvalidRole),
		errors.Is(err, domainassistant.ErrInvalidKind), errors.Is(err, domainassistant.ErrEmptyWorkspaceTitle):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	case errors.Is(err, assistant.ErrToolFailed):
		h.writeProblem(w, r, http.StatusUnprocessableEntity, "tool-failed", "Tool Failed", err.Error())
	default:
		// A provider outage is retryable: tell the client that, do not disguise it
		// as an internal error.
		if agent.RetryableError(err) || isProviderError(err) {
			h.writeProblem(w, r, http.StatusBadGateway, "provider-unavailable", "Model Provider Unavailable",
				assistant.FailureMessage(err))
			return
		}
		h.writeProblem(w, r, http.StatusBadGateway, "assistant-error", "Assistant Error", err.Error())
	}
}

func isProviderError(err error) bool {
	var providerErr *agent.ProviderError
	return errors.As(err, &providerErr)
}
