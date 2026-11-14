package tools

import (
	"context"
	"encoding/json"
	"fmt"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
)

func registerDocumentTools(registry *Registry, service *appdocuments.Service) {
	registry.Register(Tool{
		Spec: assistantSpec("read_document", "Прочитать прикреплённый документ в Markdown. Документ уже сконвертирован; используй offset для длинных документов.",
			objectSchema(map[string]any{
				"id":     integerProp("ID прикреплённого документа"),
				"offset": integerProp("Смещение в символах Markdown, по умолчанию 0"),
				"limit":  integerProp("Сколько символов вернуть, 1-20000, по умолчанию 8000"),
			}, "id")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID     int64 `json:"id"`
				Offset int   `json:"offset"`
				Limit  int   `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if args.Offset < 0 {
				args.Offset = 0
			}
			if args.Limit <= 0 {
				args.Limit = 8000
			}
			if args.Limit > 20000 {
				args.Limit = 20000
			}
			view, err := service.Get(ctx, args.ID)
			if err != nil {
				return nil, err
			}
			if string(view.Document.Status) != "processed" || view.Document.ExtractedText == nil {
				return nil, fmt.Errorf("документ ещё не сконвертирован")
			}
			runes := []rune(*view.Document.ExtractedText)
			total := len(runes)
			if args.Offset >= total {
				return map[string]any{
					"id": view.Document.ID, "filename": view.Document.Filename, "content": "", "total_chars": total, "next_offset": total,
				}, nil
			}
			end := args.Offset + args.Limit
			if end > total {
				end = total
			}
			return map[string]any{
				"id":          view.Document.ID,
				"filename":    view.Document.Filename,
				"content":     string(runes[args.Offset:end]),
				"total_chars": total,
				"next_offset": end,
			}, nil
		},
	})
}
