package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// The migration must clean up routes written by the previous, weaker guard:
// before the tool fix, a delegation from a directly opened project chat could
// bind a workspace to that project chat, and rows could keep pointing at
// conversations that no longer exist. Such routes are cleared on upgrade;
// the first delegation from the main chat re-binds them.
func TestMigrationClearsLegacyBrokenRoutes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose.SetDialect failed: %v", err)
	}
	// Stop before the route-guard migration.
	if err := goose.UpTo(db, "migrations", 23); err != nil {
		t.Fatalf("goose.UpTo 23 failed: %v", err)
	}

	// Legacy data: a valid route to the main chat, a route to a project
	// chat (from the pre-fix behavior) and a route to a conversation that
	// does not exist.
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

	// The main conversation's own row must be untouched by the cleanup.
	if _, err := db.Exec(`INSERT INTO conversations (kind, title, source_conversation_id, created_at, updated_at)
		VALUES ('main', 'Главный с маршрутом', 1, 0, 0)`); err != nil {
		t.Fatalf("seed main with route: %v", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("goose.Up failed: %v", err)
	}

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
		got := source == nil
		if got != wantCleared {
			t.Fatalf("route of %q: cleared = %v, want %v", title, got, wantCleared)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}
