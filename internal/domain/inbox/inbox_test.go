package inbox

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewItemValidation(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	item, err := NewItem("Купить молоко", json.RawMessage(`{"source":"widget"}`), now)
	if err != nil {
		t.Fatalf("valid item rejected: %v", err)
	}
	if !item.CreatedAt.Equal(now) {
		t.Errorf("created_at = %v, want %v", item.CreatedAt, now)
	}
	if item.IsProcessed() {
		t.Error("new item is already processed")
	}

	for _, text := range []string{"", strings.Repeat("x", 2001)} {
		if _, err := NewItem(text, nil, now); !errors.Is(err, ErrInvalidText) {
			t.Errorf("text of %d runes: error = %v, want ErrInvalidText", len([]rune(text)), err)
		}
	}

	if _, err := NewItem("ok", json.RawMessage(`{`), now); !errors.Is(err, ErrInvalidMeta) {
		t.Errorf("invalid meta error = %v, want ErrInvalidMeta", err)
	}

	// Length is measured in code points, not bytes.
	if _, err := NewItem(strings.Repeat("я", 2000), nil, now); err != nil {
		t.Errorf("2000 Cyrillic runes rejected: %v", err)
	}
}

func TestMarkProcessed(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	item, err := NewItem("Позвонить", nil, now)
	if err != nil {
		t.Fatalf("new item: %v", err)
	}

	item.MarkProcessed(now.Add(time.Minute), 42)
	if !item.IsProcessed() {
		t.Fatal("item is not processed after MarkProcessed")
	}
	if item.ConvertedTaskID == nil || *item.ConvertedTaskID != 42 {
		t.Errorf("converted task id = %v", item.ConvertedTaskID)
	}
	if item.ProcessedAt == nil || !item.ProcessedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("processed_at = %v", item.ProcessedAt)
	}
}
