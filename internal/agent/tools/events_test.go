package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/infrastructure/sqlite"
)

func TestEventToolsCreateAndList(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/tools.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	eventService := appevents.NewService(sqlite.NewEventUnitOfWork(db), nil, appevents.WithTimezone("Europe/Moscow"))
	registry := NewRegistry()
	registerEventTools(registry, eventService, "Europe/Moscow")

	created, err := registry.Execute(context.Background(), "create_event", json.RawMessage(`{
		"title":"Синк с командой",
		"calendar":"work",
		"starts_at":"2026-11-17T10:00:00+03:00",
		"ends_at":"2026-11-17T11:00:00+03:00"
	}`))
	if err != nil {
		t.Fatalf("create event tool: %v", err)
	}
	view, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("unexpected create result: %#v", created)
	}
	if view["title"] != "Синк с командой" {
		t.Errorf("unexpected title: %v", view["title"])
	}

	listed, err := registry.Execute(context.Background(), "list_events", json.RawMessage(`{"from":"2026-11-01","to":"2026-11-30"}`))
	if err != nil {
		t.Fatalf("list events tool: %v", err)
	}
	listView, ok := listed.(map[string]any)
	if !ok {
		t.Fatalf("unexpected list result: %#v", listed)
	}
	items, ok := listView["events"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one event, got %#v", listView["events"])
	}

	preview, err := registry.Preview("create_event", json.RawMessage(`{"title":"Синк с командой","starts_at":"2026-11-17T10:00:00+03:00"}`))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview == "" {
		t.Fatal("expected a non-empty preview")
	}
}

func TestBulkCreateEventsCreatesManyInOneCall(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/bulk.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	eventService := appevents.NewService(sqlite.NewEventUnitOfWork(db), nil, appevents.WithTimezone("Europe/Moscow"))
	registry := NewRegistry()
	registerEventTools(registry, eventService, "Europe/Moscow")

	found := false
	for _, spec := range registry.Specs() {
		if spec.Name == "create_events" {
			found = true
		}
	}
	if !found {
		t.Fatal("create_events tool must be registered")
	}

	var events []string
	for day := 1; day <= 6; day++ {
		events = append(events, `{
			"title":"Пара `+string(rune('0'+day))+`",
			"calendar":"work",
			"starts_at":"2026-11-1`+string(rune('0'+day))+`T10:00:00+03:00",
			"ends_at":"2026-11-1`+string(rune('0'+day))+`T11:30:00+03:00"
		}`)
	}
	payload := `{"events":[` + strings.Join(events, ",") + `]}`
	result, err := registry.Execute(context.Background(), "create_events", json.RawMessage(payload))
	if err != nil {
		t.Fatalf("create_events: %v", err)
	}
	view, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result: %#v", result)
	}
	if view["created_count"] != 6 {
		t.Fatalf("expected 6 created events, got %v (failed: %v)", view["created_count"], view["failed"])
	}
	if view["failed_count"] != 0 {
		t.Fatalf("unexpected failures: %v", view["failed"])
	}

	listed, err := eventService.ListRange(context.Background(),
		mustTime(t, "2026-11-01T00:00:00+03:00"), mustTime(t, "2026-11-30T00:00:00+03:00"), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 6 {
		t.Fatalf("expected 6 events in the calendar, got %d", len(listed))
	}

	preview, err := registry.Preview("create_events", json.RawMessage(`{"events":[{"title":"Пара 1","starts_at":"2026-11-11T10:00:00+03:00"},{"title":"Пара 2","starts_at":"2026-11-12T10:00:00+03:00"}]}`))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(preview, "2") || !strings.Contains(preview, "Пара 1") {
		t.Fatalf("unexpected preview: %q", preview)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func TestEventToolsUpdateAndDelete(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/edit.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	eventService := appevents.NewService(sqlite.NewEventUnitOfWork(db), nil, appevents.WithTimezone("Europe/Moscow"))
	registry := NewRegistry()
	registerEventTools(registry, eventService, "Europe/Moscow")

	created, err := registry.Execute(context.Background(), "create_event", json.RawMessage(`{
		"title":"Черновик",
		"calendar":"work",
		"starts_at":"2026-11-17T10:00:00+03:00",
		"ends_at":"2026-11-17T11:00:00+03:00"
	}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.(map[string]any)["id"].(int64)

	updated, err := registry.Execute(context.Background(), "update_event", json.RawMessage(`{
		"id":`+itoa(id)+`,
		"title":"Совещание",
		"starts_at":"2026-11-17T19:00:00+03:00",
		"ends_at":"2026-11-17T20:30:00+03:00"
	}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	view := updated.(map[string]any)
	if view["title"] != "Совещание" {
		t.Fatalf("title was not updated: %v", view["title"])
	}
	if view["starts_at"] != "2026-11-17T16:00:00Z" {
		t.Fatalf("start was not updated: %v", view["starts_at"])
	}

	if _, err := registry.Execute(context.Background(), "delete_event", json.RawMessage(`{"id":`+itoa(id)+`}`)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := eventService.Get(context.Background(), id); !appevents.NotFound(err) {
		t.Fatalf("event still readable after delete: %v", err)
	}

	// A deleted event must not appear in a calendar range read either.
	listed, err := eventService.ListRange(context.Background(),
		mustTime(t, "2026-11-01T00:00:00+03:00"), mustTime(t, "2026-11-30T00:00:00+03:00"), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("deleted event is still listed: %d", len(listed))
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
