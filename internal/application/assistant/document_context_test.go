package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

type fakeDocuments struct {
	texts map[int64]assistant.DocumentText
	err   error
	asked []int64
}

func (f *fakeDocuments) DocumentText(_ context.Context, id int64) (assistant.DocumentText, error) {
	f.asked = append(f.asked, id)
	if f.err != nil {
		return assistant.DocumentText{}, f.err
	}
	value, ok := f.texts[id]
	if !ok {
		return assistant.DocumentText{}, errors.New("document not found")
	}
	return value, nil
}

func lastUserMessage(t *testing.T, messages []assistant.ChatMessage) assistant.ChatMessage {
	t.Helper()
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return messages[index]
		}
	}
	t.Fatal("no user message was sent to the provider")
	return assistant.ChatMessage{}
}

func TestAttachedDocumentIsPassedToTheModel(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Прочитал расписание."}}}
	documents := &fakeDocuments{texts: map[int64]assistant.DocumentText{
		6: {ID: 6, Filename: "ras_z_uts13121.xls", Text: "| Дата | Предмет |\n| 12.11 | Цифровая грамотность |"},
	}}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}},
		assistant.WithDocumentTextProvider(documents))

	_, err := service.SendMessageWithInteraction(context.Background(), 0, "вот файл с расписанием", assistant.InteractionContext{
		Documents: []assistant.DocumentRef{{ID: 6, Filename: "ras_z_uts13121.xls", MarkdownChars: 40}},
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	user := lastUserMessage(t, provider.last)
	if !strings.Contains(user.Content, "Цифровая грамотность") {
		t.Fatalf("document text did not reach the model: %q", user.Content)
	}
	if !strings.Contains(user.Content, "[Документ #6 «ras_z_uts13121.xls»") {
		t.Fatalf("document header missing: %q", user.Content)
	}
	if len(documents.asked) != 1 || documents.asked[0] != 6 {
		t.Fatalf("document was not loaded by id: %+v", documents.asked)
	}
}

func TestAttachedDocumentStaysInContextOnLaterTurns(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Записал."}, {Content: "Да, в 10:00."}}}
	documents := &fakeDocuments{texts: map[int64]assistant.DocumentText{
		6: {ID: 6, Filename: "ras.xls", Text: "Зачёт по цифровой грамотности в 10:00"},
	}}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}},
		assistant.WithDocumentTextProvider(documents))

	first, err := service.SendMessageWithInteraction(context.Background(), 0, "вот файл", assistant.InteractionContext{
		Documents: []assistant.DocumentRef{{ID: 6, Filename: "ras.xls"}},
	})
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if _, err := service.SendMessageWithInteraction(context.Background(), first.ConversationID, "во сколько зачёт?", assistant.InteractionContext{}); err != nil {
		t.Fatalf("second message: %v", err)
	}

	joined := make([]string, 0, len(provider.last))
	for _, message := range provider.last {
		joined = append(joined, message.Content)
	}
	if !strings.Contains(strings.Join(joined, "\n"), "цифровой грамотности в 10:00") {
		t.Fatalf("attached document disappeared from later turns: %v", joined)
	}
}

func TestLargeAttachedDocumentIsTruncatedWithHint(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Хорошо."}}}
	long := strings.Repeat("а", assistant.MaxDocumentPromptChars+500)
	documents := &fakeDocuments{texts: map[int64]assistant.DocumentText{
		9: {ID: 9, Filename: "big.md", Text: long},
	}}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}},
		assistant.WithDocumentTextProvider(documents))

	if _, err := service.SendMessageWithInteraction(context.Background(), 0, "разбери файл", assistant.InteractionContext{
		Documents: []assistant.DocumentRef{{ID: 9, Filename: "big.md"}},
	}); err != nil {
		t.Fatalf("send message: %v", err)
	}

	user := lastUserMessage(t, provider.last)
	if !strings.Contains(user.Content, "read_document") {
		t.Fatalf("truncated document must hint at read_document: %q", user.Content[:200])
	}
	if len([]rune(user.Content)) > assistant.MaxDocumentPromptChars+2000 {
		t.Fatalf("document prompt is not bounded: %d chars", len([]rune(user.Content)))
	}
}

func TestUnavailableDocumentIsReportedToTheModel(t *testing.T) {
	provider := &scriptedProvider{responses: []assistant.Completion{{Content: "Не смог прочитать."}}}
	documents := &fakeDocuments{err: errors.New("документ ещё не сконвертирован")}
	service, _ := newServiceWithOptions(t, provider, &fakeTools{readOnly: map[string]bool{}},
		assistant.WithDocumentTextProvider(documents))

	if _, err := service.SendMessageWithInteraction(context.Background(), 0, "прочитай файл", assistant.InteractionContext{
		Documents: []assistant.DocumentRef{{ID: 3, Filename: "broken.pdf"}},
	}); err != nil {
		t.Fatalf("send message: %v", err)
	}

	user := lastUserMessage(t, provider.last)
	if !strings.Contains(user.Content, "недоступен") {
		t.Fatalf("unavailable document must be reported: %q", user.Content)
	}
}
