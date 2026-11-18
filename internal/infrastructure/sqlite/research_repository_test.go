package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

func setupResearchDB(t *testing.T) (*ResearchUnitOfWork, *sql.DB) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "lifeos.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return NewResearchUnitOfWork(db), db
}

// insertMemoryEntry stores one knowledge entry so a report can point at it.
func insertMemoryEntry(t *testing.T, db *sql.DB, key string) int64 {
	t.Helper()
	now := time.Now().UTC().Unix()
	result, err := db.Exec(`INSERT INTO memory_entries
		(area, category, title, normalized_title, content, dedupe_key, certainty, confidence, status, entities, created_at, updated_at, version)
		VALUES ('knowledge','research','t','t','c',?,'assumption',0.5,'active','[]',?,?,1)`, key, now, now)
	if err != nil {
		t.Fatalf("insert memory entry: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read memory entry id: %v", err)
	}
	return id
}

func researchReport(at time.Time) *domainresearch.Report {
	return &domainresearch.Report{
		Question:  "Что нового в Go 1.24?",
		Queries:   []string{"go 1.24 release notes", "go 1.24 generic type aliases"},
		Status:    domainresearch.ReportRunning,
		StartedAt: at,
		Version:   1,
	}
}

func TestResearchRepositoryReportRoundTrip(t *testing.T) {
	uow, db := setupResearchDB(t)
	ctx := context.Background()
	at := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	report := researchReport(at)

	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		id, err := tx.Research().InsertReport(ctx, report)
		if err != nil {
			return err
		}
		if id != report.ID || id == 0 {
			t.Errorf("insert returned id %d, report id %d", id, report.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("insert report: %v", err)
	}

	finished := at.Add(2 * time.Minute)
	report.Status = domainresearch.ReportCompleted
	report.FinishedAt = &finished
	report.Summary = "## Итог\n\nПодтверждено."
	report.Findings = []domainresearch.Finding{{
		Statement:     "Go 1.24 adds generic type aliases.",
		Certainty:     domainresearch.CertaintyFact,
		Confidence:    0.8,
		Corroboration: 2,
		Domains:       []string{"go.dev", "example.org"},
		Citations: []domainresearch.Citation{
			{URL: "https://go.dev/doc/go1.24", Title: "Go 1.24", Site: "go.dev", Excerpt: "…", FetchedAt: at, Trust: domainresearch.TrustHigh},
			{URL: "https://example.org/go", Title: "Review", Site: "example.org", Excerpt: "…", FetchedAt: at, Trust: domainresearch.TrustNormal},
		},
	}}
	report.Sources = 2
	entryID := insertMemoryEntry(t, db, "report-link")
	report.KnowledgeEntryID = &entryID

	var updated *domainresearch.Report
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		updated, err = tx.Research().UpdateReport(ctx, report.ID, 1, report)
		return err
	}); err != nil {
		t.Fatalf("update report: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	var loaded *domainresearch.Report
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		loaded, err = tx.Research().GetReport(ctx, report.ID)
		return err
	}); err != nil {
		t.Fatalf("get report: %v", err)
	}
	if loaded.Status != domainresearch.ReportCompleted || loaded.Sources != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if len(loaded.Findings) != 1 || loaded.Findings[0].Certainty != domainresearch.CertaintyFact {
		t.Fatalf("findings lost: %+v", loaded.Findings)
	}
	if len(loaded.Findings[0].Citations) != 2 {
		t.Fatalf("citations lost: %+v", loaded.Findings[0].Citations)
	}
	if loaded.KnowledgeEntryID == nil || *loaded.KnowledgeEntryID != entryID {
		t.Fatalf("knowledge link lost: %+v", loaded.KnowledgeEntryID)
	}
	if loaded.FinishedAt == nil || !loaded.FinishedAt.Equal(finished) {
		t.Fatalf("finished_at = %v", loaded.FinishedAt)
	}
}

func TestResearchRepositoryConditionalWriteAndList(t *testing.T) {
	uow, _ := setupResearchDB(t)
	ctx := context.Background()
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		report := researchReport(base.Add(time.Duration(i) * time.Hour))
		report.Question = "Вопрос " + string(rune('A'+i))
		if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
			_, err := tx.Research().InsertReport(ctx, report)
			return err
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	var (
		reports []*domainresearch.Report
		total   int
	)
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		reports, total, err = tx.Research().ListReports(ctx, appresearch.ReportFilter{Limit: 2})
		return err
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(reports) != 2 {
		t.Fatalf("listed %d of %d", len(reports), total)
	}
	if reports[0].Question != "Вопрос C" {
		t.Fatalf("newest first, got %q", reports[0].Question)
	}

	// A stale version must be refused instead of overwriting.
	stale := *reports[0]
	stale.Summary = "старое"
	err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		_, err := tx.Research().UpdateReport(ctx, stale.ID, stale.Version+5, &stale)
		return err
	})
	if !errors.Is(err, appresearch.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}

	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		return tx.Research().DeleteReport(ctx, reports[0].ID)
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	err = uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		return tx.Research().DeleteReport(ctx, reports[0].ID)
	})
	if !errors.Is(err, appresearch.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestResearchRepositoryConnectionFilters(t *testing.T) {
	uow, _ := setupResearchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	makeConnection := func(name string, kind domainresearch.SourceKind, enabled bool) *domainresearch.Connection {
		return &domainresearch.Connection{
			Name: name, Kind: kind, Mode: domainresearch.ModeRead, Endpoint: "https://api.example.com",
			Trust: domainresearch.TrustNormal, Enabled: enabled, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		for _, connection := range []*domainresearch.Connection{
			makeConnection("On", domainresearch.KindHTTPAPI, true),
			makeConnection("Off", domainresearch.KindHTTPAPI, false),
			makeConnection("MCP", domainresearch.KindMCP, true),
		} {
			if _, err := tx.Research().InsertConnection(ctx, connection); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("insert connections: %v", err)
	}

	enabled := true
	var found []*domainresearch.Connection
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		found, err = tx.Research().ListConnections(ctx, appresearch.ConnectionFilter{Enabled: &enabled})
		return err
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("enabled sources = %d", len(found))
	}

	kind := domainresearch.KindMCP
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		found, err = tx.Research().ListConnections(ctx, appresearch.ConnectionFilter{Kind: &kind})
		return err
	}); err != nil {
		t.Fatalf("list by kind: %v", err)
	}
	if len(found) != 1 || found[0].Name != "MCP" {
		t.Fatalf("sources by kind = %+v", found)
	}
}

func TestResearchRepositoryWatchesAreDueAndVersioned(t *testing.T) {
	uow, _ := setupResearchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	due := now.Add(-time.Minute)
	watch := &domainresearch.Watch{
		Topic: "Go releases", Query: "go release notes", Queries: []string{"go release notes"},
		Cadence: domainresearch.CadenceDaily, Enabled: true, NextCheckAt: &due,
		Seen:      []domainresearch.Fingerprint{{URL: "https://go.dev", Hash: "abc", FirstSeen: now.Add(-time.Hour)}},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		_, err := tx.Research().InsertWatch(ctx, watch)
		return err
	}); err != nil {
		t.Fatalf("insert watch: %v", err)
	}

	var dueWatches []*domainresearch.Watch
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		dueWatches, err = tx.Research().DueWatches(ctx, now)
		return err
	}); err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(dueWatches) != 1 || dueWatches[0].ID != watch.ID {
		t.Fatalf("due = %+v", dueWatches)
	}
	if len(dueWatches[0].Seen) != 1 || dueWatches[0].Seen[0].URL != "https://go.dev" {
		t.Fatalf("baseline lost: %+v", dueWatches[0].Seen)
	}

	next := now.Add(24 * time.Hour)
	changed := now
	watch.NextCheckAt = &next
	watch.LastCheckedAt = &now
	watch.LastChangeAt = &changed
	watch.LastChangeSummary = "новых источников: 1"
	var updated *domainresearch.Watch
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		updated, err = tx.Research().UpdateWatch(ctx, watch.ID, 1, watch)
		return err
	}); err != nil {
		t.Fatalf("update watch: %v", err)
	}
	if updated.Version != 2 || updated.LastChangeSummary != "новых источников: 1" {
		t.Fatalf("updated = %+v", updated)
	}

	err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		_, err := tx.Research().UpdateWatch(ctx, watch.ID, 1, watch)
		return err
	})
	if !errors.Is(err, appresearch.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}

	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		dueWatches, err := tx.Research().DueWatches(ctx, now)
		if err != nil {
			return err
		}
		if len(dueWatches) != 0 {
			t.Errorf("the watch was rescheduled, still due: %+v", dueWatches)
		}
		return nil
	}); err != nil {
		t.Fatalf("due after update: %v", err)
	}
}

func TestResearchRepositoryJournalsCalls(t *testing.T) {
	uow, _ := setupResearchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		for _, outcome := range []string{appresearch.CallOK, appresearch.CallRefused, appresearch.CallFailed} {
			if _, err := tx.Research().RecordCall(ctx, &appresearch.CallRecord{
				ConnectionID: 7, Name: "API", Kind: domainresearch.KindHTTPAPI,
				Mode: domainresearch.ModeRead, Method: "GET", Operation: "/items", Outcome: outcome,
				Detail: "detail", CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("record calls: %v", err)
	}

	var (
		records []*appresearch.CallRecord
		total   int
	)
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		records, total, err = tx.Research().ListCalls(ctx, 7, 2)
		return err
	}); err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d", total)
	}
	if len(records) != 2 {
		t.Fatalf("page size = %d", len(records))
	}
	if records[0].Kind != domainresearch.KindHTTPAPI || records[0].Mode != domainresearch.ModeRead {
		t.Fatalf("record = %+v", records[0])
	}
}

func TestMemorySourceWebKindIsAccepted(t *testing.T) {
	_, db := setupResearchDB(t)
	entryID := insertMemoryEntry(t, db, "key-web")
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`INSERT INTO memory_sources (entry_id, kind, source_id, label, excerpt, observed_at, created_at)
		VALUES (?, 'web', 0, 'Веб: example.org', 'fragment', ?, ?)`, entryID, now, now); err != nil {
		t.Fatalf("the web provenance kind must be accepted: %v", err)
	}
}

// The journal of a changing call starts as an intent and is closed with the
// outcome; the method is recorded so a read can be told from a deletion.
func TestResearchRepositoryClosesAWriteAheadCall(t *testing.T) {
	uow, _ := setupResearchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	var id int64
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		id, err = tx.Research().RecordCall(ctx, &appresearch.CallRecord{
			ConnectionID: 3, Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
			Method: "DELETE", Operation: "/items/1", Outcome: appresearch.CallStarted,
			Detail: "отправляется", CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if id == 0 {
		t.Fatal("the intent must be stored and identified")
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		return tx.Research().FinishCall(ctx, id, appresearch.CallOK, "204 No Content", 42)
	}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		// A second close must not overwrite the outcome.
		return tx.Research().FinishCall(ctx, id, appresearch.CallFailed, "поздно", 1)
	}); !errors.Is(err, appresearch.ErrNotFound) {
		t.Fatalf("a closed entry must not be closed again, got %v", err)
	}

	var records []*appresearch.CallRecord
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		records, _, err = tx.Research().ListCalls(ctx, 3, 10)
		return err
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("one call is one entry, got %+v", records)
	}
	if records[0].Outcome != appresearch.CallOK || records[0].Method != "DELETE" || records[0].DurationMS != 42 {
		t.Fatalf("entry = %+v", records[0])
	}
}

// A restart closes what the previous process left open: a running report is
// failed with a reason, and an unclosed changing call becomes unknown.
func TestResearchRepositoryRecoversInterruptedWork(t *testing.T) {
	uow, db := setupResearchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	var reportID int64
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		reportID, err = tx.Research().InsertReport(ctx, &domainresearch.Report{
			Question: "Что осталось в работе?", Queries: []string{"вопрос"},
			Status: domainresearch.ReportRunning, StartedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		_, err := tx.Research().RecordCall(ctx, &appresearch.CallRecord{
			ConnectionID: 4, Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
			Method: "POST", Operation: "/items", Outcome: appresearch.CallStarted, CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("record call: %v", err)
	}

	var reports, calls int64
	if err := uow.Do(ctx, func(ctx context.Context, tx appresearch.Transaction) error {
		var err error
		if reports, err = tx.Research().RecoverRunningReports(ctx, "прервано перезапуском", now); err != nil {
			return err
		}
		calls, err = tx.Research().RecoverStartedCalls(ctx, "результат неизвестен", now)
		return err
	}); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if reports != 1 || calls != 1 {
		t.Fatalf("recovered %d reports and %d calls", reports, calls)
	}

	var status, reportError string
	if err := db.QueryRow(`SELECT status, error FROM research_reports WHERE id = ?`, reportID).Scan(&status, &reportError); err != nil {
		t.Fatalf("read report: %v", err)
	}
	if status != string(domainresearch.ReportFailed) || reportError != "прервано перезапуском" {
		t.Fatalf("report = %s / %q", status, reportError)
	}
	var outcome string
	if err := db.QueryRow(`SELECT outcome FROM research_calls WHERE connection_id = 4`).Scan(&outcome); err != nil {
		t.Fatalf("read call: %v", err)
	}
	if outcome != appresearch.CallUnknown {
		t.Fatalf("call outcome = %q", outcome)
	}
}
