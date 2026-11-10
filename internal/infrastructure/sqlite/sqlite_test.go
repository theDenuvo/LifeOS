package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose.SetDialect failed: %v", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("goose.Up failed: %v", err)
	}

	return db
}

func TestMigrationsApply(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	version, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("goose.GetDBVersion failed: %v", err)
	}
	if version != 9 {
		t.Errorf("Expected version 9, got %d", version)
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := goose.Up(db, "migrations")
	if err != nil {
		t.Fatalf("Second goose.Up failed: %v", err)
	}

	version, _ := goose.GetDBVersion(db)
	if version != 9 {
		t.Errorf("Expected version 9 after re-apply, got %d", version)
	}
}

func TestSchemaTablesExist(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	expectedTables := []string{
		"tasks", "inbox_items", "task_events", "conversations",
		"messages", "pending_actions", "app_settings", "goose_db_version",
	}

	for _, table := range expectedTables {
		var exists int
		err := db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists)
		if err != nil {
			t.Errorf("Table %q missing: %v", table, err)
		}
	}
}

func TestSchemaIndexesExist(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	expectedIndexes := []string{
		"idx_tasks_status", "idx_tasks_priority", "idx_tasks_planned_date",
		"idx_tasks_due_date", "idx_tasks_due_at", "idx_tasks_created_at",
		"idx_tasks_parent", "idx_inbox_created_at", "idx_inbox_converted_task",
		"idx_task_events_task_id", "idx_task_events_type",
		"idx_messages_conversation", "idx_pending_actions_conversation",
		"idx_pending_actions_status",
	}

	for _, index := range expectedIndexes {
		var exists int
		err := db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&exists)
		if err != nil {
			t.Errorf("Index %q missing: %v", index, err)
		}
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	var fkEnabled int
	err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("PRAGMA foreign_keys failed: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("Foreign keys not enabled")
	}
}

func TestWALModeEnabled(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	var journalMode string
	err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode failed: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got %q", journalMode)
	}
}

func TestTimestampStorageClass(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Test', 'medium', 'open')`)

	var createdAtType string
	err := db.QueryRow("SELECT typeof(created_at) FROM tasks WHERE id = 1").Scan(&createdAtType)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if createdAtType != "integer" {
		t.Errorf("created_at storage class = %q, want 'integer'", createdAtType)
	}
}

func TestTaskEventsAppendOnly(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Test', 'medium', 'open')`)
	_, _ = db.Exec(`INSERT INTO task_events (task_id, type, payload, actor) VALUES (1, 'created', '{}', 'user')`)

	_, err := db.Exec(`UPDATE task_events SET payload = '{}' WHERE task_id = 1`)
	if err == nil {
		t.Fatal("UPDATE on task_events should have been blocked")
	}

	_, err = db.Exec(`DELETE FROM task_events WHERE task_id = 1`)
	if err == nil {
		t.Fatal("DELETE on task_events should have been blocked")
	}
}

func TestTaskDeleteWithHistoryBlocked(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Test', 'medium', 'open')`)
	_, _ = db.Exec(`INSERT INTO task_events (task_id, type, payload, actor) VALUES (1, 'created', '{}', 'user')`)

	_, err := db.Exec(`DELETE FROM tasks WHERE id = 1`)
	if err == nil {
		t.Fatal("DELETE task with history should have been blocked")
	}
}

func TestTaskDeleteWithoutHistoryAllowed(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Test', 'medium', 'open')`)

	_, err := db.Exec(`DELETE FROM tasks WHERE id = 1`)
	if err != nil {
		t.Fatalf("DELETE task without history should be allowed: %v", err)
	}
}

func TestTaskEventRestoredType(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Test', 'medium', 'open')`)
	_, err := db.Exec(`INSERT INTO task_events (task_id, type, payload, actor) VALUES (1, 'restored', '{}', 'user')`)
	if err != nil {
		t.Fatalf("'restored' event type should be allowed: %v", err)
	}
}

func TestInboxConvertedTaskId(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Task', 'medium', 'open')`)
	_, _ = db.Exec(`INSERT INTO inbox_items (text, converted_task_id) VALUES ('Item', 1)`)

	var taskID int
	err := db.QueryRow(`SELECT converted_task_id FROM inbox_items WHERE id = 1`).Scan(&taskID)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if taskID != 1 {
		t.Errorf("converted_task_id = %d, want 1", taskID)
	}

	_, _ = db.Exec(`DELETE FROM tasks WHERE id = 1`)

	var nullVal interface{}
	scanErr := db.QueryRow(`SELECT converted_task_id FROM inbox_items WHERE id = 1`).Scan(&nullVal)
	if scanErr != nil {
		t.Fatalf("Query failed: %v", scanErr)
	}
	if nullVal != nil {
		t.Errorf("converted_task_id should be NULL after task delete, got %v", nullVal)
	}
}

func TestInboxConvertTransaction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// First create an inbox item to convert
	_, err := db.Exec(`INSERT INTO inbox_items (text) VALUES ('Test item')`)
	if err != nil {
		t.Fatalf("Insert inbox item failed: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	_, err = tx.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('Converted', 'medium', 'open')`)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Insert task failed: %v", err)
	}

	_, err = tx.Exec(`UPDATE inbox_items SET converted_task_id = 1, processed_at = strftime('%s','now') WHERE id = 1`)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Update inbox failed: %v", err)
	}

	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	var taskID int
	err = db.QueryRow(`SELECT converted_task_id FROM inbox_items WHERE id = 1`).Scan(&taskID)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if taskID != 1 {
		t.Errorf("converted_task_id = %d, want 1", taskID)
	}
}
