package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
)

// StreamEvent is one server-sent assistant event.
type StreamEvent struct {
	Type string
	Data any
}

// SendMessageStream runs one assistant turn and emits incremental events.
func (s *Service) SendMessageStream(
	ctx context.Context,
	conversationID int64,
	content string,
	interaction InteractionContext,
	model string,
	autoConfirm bool,
	emit func(StreamEvent) error,
) (SendResult, error) {
	if s.provider == nil {
		return SendResult{}, ErrProviderNotConfigured
	}
	if !utf8.ValidString(content) {
		return SendResult{}, domainassistant.ErrInvalidContent
	}
	content = strings.TrimSpace(content)
	if length := utf8.RuneCountInString(content); length == 0 || length > 8000 {
		return SendResult{}, domainassistant.ErrInvalidContent
	}

	provider := s.provider
	if model != "" {
		if aware, ok := provider.(ModelAwareProvider); ok {
			provider = aware.WithModel(model)
		}
	}
	// Surface retries and model fallbacks as stream events: a provider hiccup is
	// not the end of the turn.
	if observer, ok := provider.(RetryObserverProvider); ok {
		provider = observer.WithRetryObserver(func(retry ProviderRetry) {
			_ = emitEvent(emit, StreamEvent{Type: "retry", Data: map[string]any{
				"model":    retry.Model,
				"attempt":  retry.Attempt,
				"fallback": retry.Fallback,
				"reason":   retry.Reason,
			}})
		})
	}

	now := s.clock.Now()
	var (
		history []*domainassistant.Message
		userID  int64
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Assistant()
		if conversationID == 0 {
			title := truncateRunes(content, 80)
			conversation := domainassistant.NewConversation(title, now)
			id, err := repo.CreateConversation(ctx, conversation)
			if err != nil {
				return err
			}
			conversationID = id
		} else if _, err := repo.GetConversation(ctx, conversationID); err != nil {
			return err
		}
		userMessage, err := domainassistant.NewMessage(conversationID, domainassistant.RoleUser, content, now)
		if err != nil {
			return err
		}
		userMessage.Attachments = documentAttachments(interaction.Documents)
		id, err := insertUserMessage(ctx, repo, userMessage)
		if err != nil {
			return err
		}
		userID = id
		if err := repo.TouchConversation(ctx, conversationID, now); err != nil {
			return err
		}
		loaded, err := repo.ListMessages(ctx, conversationID, s.historyLimit)
		if err != nil {
			return err
		}
		history = loaded
		return nil
	})
	if err != nil {
		return SendResult{}, err
	}

	emitEvent(emit, StreamEvent{Type: "start", Data: map[string]any{
		"conversation_id": conversationID,
		"user_message_id": userID,
	}})

	snapshot := ""
	contextUnavailable := false
	if s.context != nil {
		value, err := s.context.Snapshot(ctx)
		if err != nil {
			contextUnavailable = true
		} else {
			snapshot = value
		}
	}
	chat := s.buildChat(ctx, history, now, snapshot, contextUnavailable, sanitizeInteraction(interaction), autoConfirm)

	pending := make([]*domainassistant.PendingAction, 0)
	attachments := make([]domainassistant.Attachment, 0)

	for round := 0; round < s.maxRounds; round++ {
		var roundContent, roundReasoning strings.Builder
		onDelta := func(delta StreamDelta) error {
			if delta.Reasoning != "" {
				roundReasoning.WriteString(delta.Reasoning)
				if err := emitEvent(emit, StreamEvent{Type: "reasoning", Data: map[string]any{"delta": delta.Reasoning}}); err != nil {
					return err
				}
			}
			if delta.Content != "" {
				roundContent.WriteString(delta.Content)
				if err := emitEvent(emit, StreamEvent{Type: "content", Data: map[string]any{"delta": delta.Content}}); err != nil {
					return err
				}
			}
			return nil
		}

		var completion Completion
		if streamer, ok := provider.(StreamingProvider); ok {
			completion, err = streamer.StreamComplete(ctx, chat, s.tools.Specs(), onDelta)
		} else {
			completion, err = provider.Complete(ctx, chat, s.tools.Specs())
			if err == nil {
				if completion.Reasoning != "" {
					if emitErr := onDelta(StreamDelta{Reasoning: completion.Reasoning}); emitErr != nil {
						return SendResult{}, emitErr
					}
				}
				if completion.Content != "" {
					if emitErr := onDelta(StreamDelta{Content: completion.Content}); emitErr != nil {
						return SendResult{}, emitErr
					}
				}
			}
		}
		if err != nil {
			// Keep whatever the model already produced: a dropped stream or a user
			// pressing "Stop" must not erase visible text. Reasoning alone is not an
			// answer, so it never becomes a message on its own.
			if roundContent.Len() > 0 {
				_, _ = s.persistAssistantMessage(context.Background(), conversationID, roundContent.String(), nil, attachments, roundReasoning.String(), s.clock.Now())
			}
			if ctx.Err() == nil {
				_ = emitEvent(emit, StreamEvent{Type: "error", Data: map[string]any{
					"message":   FailureMessage(err),
					"retryable": true,
					"reason":    truncateRunes(err.Error(), 200),
				}})
			}
			return SendResult{}, err
		}

		if len(completion.ToolCalls) == 0 {
			text := strings.TrimSpace(completion.Content)
			if text == "" {
				text = strings.TrimSpace(roundContent.String())
			}
			if text == "" {
				text = "Готово."
			}
			if len(pending) > 0 && !mentionsConfirmation(text) {
				text = strings.TrimSpace(text) + "\n\nДействие ждёт вашего подтверждения."
			}
			reasoning := completion.Reasoning
			if reasoning == "" {
				reasoning = roundReasoning.String()
			}
			message, err := s.persistAssistantMessage(ctx, conversationID, text, nil, attachments, reasoning, s.clock.Now())
			if err != nil {
				return SendResult{}, err
			}
			result := SendResult{ConversationID: conversationID, Message: message, PendingActions: pending}
			s.publish("assistant.message", map[string]any{
				"conversation_id": conversationID,
				"message_id":      message.ID,
				"role":            string(message.Role),
				"content":         message.Content,
			})
			_ = emitEvent(emit, StreamEvent{Type: "done", Data: result})
			return result, nil
		}

		toolCallsJSON, err := json.Marshal(completion.ToolCalls)
		if err != nil {
			return SendResult{}, err
		}
		toolMessage, err := s.persistAssistantMessage(ctx, conversationID, roundContent.String(), toolCallsJSON, nil, roundReasoning.String(), s.clock.Now())
		if err != nil {
			return SendResult{}, err
		}
		chat = append(chat, ChatMessage{
			Role:      "assistant",
			Content:   roundContent.String(),
			ToolCalls: completion.ToolCalls,
		})

		for _, call := range completion.ToolCalls {
			_ = emitEvent(emit, StreamEvent{Type: "tool", Data: map[string]any{
				"name": call.Name, "status": "running",
			}})
			resultText, action, toolAttachments, err := s.runTool(ctx, conversationID, toolMessage.ID, call, s.clock.Now())
			if err != nil {
				_ = emitEvent(emit, StreamEvent{Type: "error", Data: map[string]any{"message": err.Error()}})
				return SendResult{}, err
			}
			attachments = append(attachments, toolAttachments...)
			if action != nil {
				if autoConfirm {
					confirmed, confirmErr := s.ConfirmPendingAction(ctx, action.ID)
					if confirmErr != nil {
						_ = emitEvent(emit, StreamEvent{Type: "error", Data: map[string]any{"message": confirmErr.Error()}})
						return SendResult{}, confirmErr
					}
					action = confirmed.Action
					payload, marshalErr := json.Marshal(confirmed.Result)
					if marshalErr == nil {
						resultText = string(payload)
					}
					_ = emitEvent(emit, StreamEvent{Type: "pending_action", Data: action})
				} else {
					pending = append(pending, action)
					_ = emitEvent(emit, StreamEvent{Type: "pending_action", Data: action})
				}
			}
			_ = emitEvent(emit, StreamEvent{Type: "tool", Data: map[string]any{
				"name": call.Name, "status": "done", "auto_confirmed": autoConfirm,
			}})
			chat = append(chat, ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    resultText,
			})
		}
	}

	fallback := fmt.Sprintf("Не удалось завершить за %d шагов. Уточните запрос или попробуйте ещё раз.", s.maxRounds)
	if len(pending) > 0 {
		fallback += "\n\nДействие ждёт подтверждения."
	}
	message, err := s.persistAssistantMessage(ctx, conversationID, fallback, nil, attachments, "", s.clock.Now())
	if err != nil {
		return SendResult{}, err
	}
	result := SendResult{ConversationID: conversationID, Message: message, PendingActions: pending}
	_ = emitEvent(emit, StreamEvent{Type: "done", Data: result})
	return result, nil
}

func emitEvent(emit func(StreamEvent) error, event StreamEvent) error {
	if emit == nil {
		return nil
	}
	return emit(event)
}

// FailureMessage renders a failed turn for the user. A provider outage is a
// retry signal, not a dead end: the message says so and offers the next step.
func FailureMessage(err error) string {
	base := "Модель не ответила — провайдер недоступен или ограничил запросы. Повторите отправку или выберите другую модель в шапке диалога."
	if err == nil {
		return base
	}
	if errors.Is(err, ErrProviderNotConfigured) {
		return "Провайдер моделей не настроен: задайте LIFEOS_LLM_API_KEY или локальную модель."
	}
	reason := truncateRunes(strings.TrimSpace(err.Error()), 200)
	if reason == "" {
		return base
	}
	return base + " Причина: " + reason
}
