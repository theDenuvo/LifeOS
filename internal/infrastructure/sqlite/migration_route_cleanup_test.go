package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db")+
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose.SetDialect failed: %v", err)
	}
	return db
}

func seedLegacyRoutes(t *testing.T, db *sql.DB) {
	t.Helper()
	// Legacy data: a valid route to the main chat, a route to a project chat
	// (from the pre-guard behavior) and a route to a conversation that does
	// not exist.
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, created_at, updated_at)
		VALUES ('main', 'Главный чат', 0, 0)`); err != nil {
		t.Fatalf("seed main: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('project', 'К главному чату', 1, 0, 0)`); err != nil {
		t.Fatalf("seed valid route: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, created_at, updated_at)
		VALUES ('project', 'Проектный чат', 0, 0)`); err != nil {
		t.Fatalf("seed project target: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('project', 'К проектному чату', 3, 0, 0)`); err != nil {
		t.Fatalf("seed project route: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('project', 'К несуществующему чату', 999, 0, 0)`); err != nil {
		t.Fatalf("seed dangling route: %v", err)
	}
	// A main row with a route must be untouched by the cleanup.
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('main', 'Главный с маршрутом', 1, 0, 0)`); err != nil {
		t.Fatalf("seed main with route: %v", err)
	}
}

func assertRoutesAfterRepair(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT title, source_conversation_id FROM conversations ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	kept := map[string]bool{
		"К главному чату":        false, // valid route must survive
		"Главный с маршрутом":    false, // main rows stay as they are
		"К проектному чату":      true,  // must be cleared
		"К несуществующему чату": true,  // must be cleared
	}
	for rows.Next() {
		var title string
		var source any
		if err := rows.Scan(&title, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		wantCleared, watched := kept[title]
		if !watched {
			continue
		}
		if got := source == nil; got != wantCleared {
			t.Fatalf("route of %q: cleared = %v, want %v", title, got, wantCleared)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

// Upgrade from version 23: legacy routes written before any guard existed
// (from a directly opened project chat, or to a conversation that no longer
// exists) must be cleaned by the repair migration.
func TestMigrationFrom23ClearsLegacyBrokenRoutes(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := goose.UpTo(db, "migrations", 23); err != nil {
		t.Fatalf("goose.UpTo 23 failed: %v", err)
	}
	seedLegacyRoutes(t, db)
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("goose.Up failed: %v", err)
	}
	assertRoutesAfterRepair(t, db)
}

// Upgrade from version 24: the original 024 already ran, so its weaker
// triggers are installed and goose will never re-apply it. Routes to
// non-existent conversations slipped through its NULL hole afterwards; the
// repair migration must clear them and replace the triggers.
func TestMigrationFrom24RepairsOldGuardState(t *testing.T) {
	db := openMigrationTestDB(t)
	// Version 24 with the original (weaker) 024 triggers.
	if err := goose.UpTo(db, "migrations", 24); err != nil {
		t.Fatalf("goose.UpTo 24 failed: %v", err)
	}

	// Post-024 data the weak guard let through: a route to a conversation
	// that does not exist. The kind comparison yields NULL there, so the
	// old trigger does not abort.
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, created_at, updated_at)
		VALUES ('main', 'Главный чат', 0, 0)`); err != nil {
		t.Fatalf("seed main: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('project', 'С висячим маршрутом', 999, 0, 0)`); err != nil {
		t.Fatalf("seed dangling route: %v", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("goose.Up failed: %v", err)
	}

	// The repair migration cleaned the dangling route...
	rows, err := db.Query(`SELECT source_conversation_id FROM conversations WHERE title = 'С висячим маршрутом'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("workspace row is missing")
	}
	var source any
	if err := rows.Scan(&source); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if source != nil {
		t.Fatalf("dangling route survived the upgrade: %v", source)
	}

	// ...and the repaired triggers now refuse what the old ones let through.
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('project', 'Ещё один висячий', 999, 0, 0)`); err == nil {
		t.Fatal("repaired trigger must refuse a dangling route")
	}
}
