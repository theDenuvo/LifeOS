package documents

import (
	"context"
	"strings"
	"testing"
	"time"

	appdocuments "github.com/theDenuvo/LifeOS/internal/application/documents"
)

func TestParseTasksObjectAndArray(t *testing.T) {
	for _, raw := range []string{
		`{"tasks":[{"title":"Позвонить маме","due_date":"2026-11-20","priority":"high","confidence":0.9}]}`,
		"```json\n{\"tasks\":[{\"title\":\"Позвонить маме\",\"due_date\":\"2026-11-20\",\"priority\":\"high\",\"confidence\":0.9}]}\n```",
		`[{"title":"Позвонить маме","due_date":"2026-11-20","priority":"high","confidence":0.9}]`,
	} {
		tasks, err := parseTasks(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if len(tasks) != 1 || tasks[0].Title != "Позвонить маме" || tasks[0].DueDate == nil || *tasks[0].DueDate != "2026-11-20" {
			t.Fatalf("unexpected tasks: %+v", tasks)
		}
	}
}

func TestExtractorRejectsPaidOpenRouterModel(t *testing.T) {
	extractor := NewExtractor("https://openrouter.ai/api/v1", "key", "google/gemini-4-hydrogen")
	_, err := extractor.ExtractTasks(context.Background(), appdocuments.TaskExtractionInput{
		Filename: "x.txt",
		Text:     "Задача",
		Today:    time.Now(),
		Timezone: "Europe/Moscow",
	})
	if err == nil || !strings.Contains(err.Error(), "только бесплатные") {
		t.Fatalf("expected free-model error, got %v", err)
	}
}
