package sqlite

import (
	"database/sql"
	"fmt"
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
	if version != 10 {
		t.Errorf("Expected version 10, got %d", version)
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
	if version != 10 {
		t.Errorf("Expected version 10 after re-apply, got %d", version)
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

func TestInboxConvertTransactionRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// First create an inbox item to convert
	_, err := db.Exec(`INSERT INTO inbox_items (text) VALUES ('Test item for rollback')`)
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

	// Intentionally cause an error - violate foreign key constraint
	// Try to insert task_event with non-existent task_id
	_, err = tx.Exec(`INSERT INTO task_events (task_id, type, payload, actor) VALUES (999, 'created', '{}', 'user')`)
	if err == nil {
		tx.Rollback()
		t.Fatal("Insert task_event with invalid FK should have failed")
	}

	// Rollback the transaction
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify no task was created
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE title = 'Converted'`).Scan(&count)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 tasks after rollback, got %d", count)
	}

	// Verify inbox item was not modified
	var processedAt interface{}
	err = db.QueryRow(`SELECT processed_at FROM inbox_items WHERE text = 'Test item for rollback'`).Scan(&processedAt)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if processedAt != nil {
		t.Errorf("Expected processed_at to be NULL after rollback, got %v", processedAt)
	}
}

// TestProductionMigrationPath tests migrations using real sqlite.Open and sqlite.Migrate
func TestProductionMigrationPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "prod_test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("sqlite.Migrate failed: %v", err)
	}

	version, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("goose.GetDBVersion failed: %v", err)
	}
	if version != 10 {
		t.Errorf("Expected version 10, got %d", version)
	}
}

// TestMigrationUpgrade008to009 tests upgrading from migration 008 to 009 with existing data
func TestMigrationUpgrade008to009(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upgrade_test.db")

	// Create DB and apply only up to migration 008
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		db.Close()
		t.Fatalf("goose.SetDialect failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 001
		db.Close()
		t.Fatalf("goose.UpByOne 001 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 002
		db.Close()
		t.Fatalf("goose.UpByOne 002 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 003
		db.Close()
		t.Fatalf("goose.UpByOne 003 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 004
		db.Close()
		t.Fatalf("goose.UpByOne 004 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 005
		db.Close()
		t.Fatalf("goose.UpByOne 005 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 006
		db.Close()
		t.Fatalf("goose.UpByOne 006 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 007
		db.Close()
		t.Fatalf("goose.UpByOne 007 failed: %v", err)
	}
	if err := goose.UpByOne(db, "migrations"); err != nil { // 008
		db.Close()
		t.Fatalf("goose.UpByOne 008 failed: %v", err)
	}

	// Insert test data at version 008
	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status, due_at, completed_at) VALUES 
		('Task 1', 'high', 'open', 1700000000, NULL),
		('Task 2', 'medium', 'completed', 1700000000, 1700000001),
		('Task 3', 'low', 'cancelled', NULL, 1700000002)`)
	_, _ = db.Exec(`INSERT INTO inbox_items (text, meta) VALUES 
		('Inbox 1', '{}'),
		('Inbox 2', '{"source": "test"}')`)
	_, _ = db.Exec(`INSERT INTO task_events (task_id, type, payload, actor) VALUES 
		(1, 'created', '{}', 'user'),
		(2, 'created', '{}', 'user'),
		(2, 'completed', '{"old_status": "open"}', 'user')`)
	_, _ = db.Exec(`INSERT INTO conversations (title) VALUES ('Conv 1')`)
	_, _ = db.Exec(`INSERT INTO messages (conversation_id, role, content) VALUES (1, 'user', 'Hello')`)
	_, _ = db.Exec(`INSERT INTO pending_actions (conversation_id, message_id, tool_name, arguments, preview, status) VALUES (1, 1, 'create_task', '{}', 'Create task', 'pending')`)

	// Now apply migration 009
	if err := goose.UpByOne(db, "migrations"); err != nil {
		db.Close()
		t.Fatalf("goose.UpByOne 009 failed: %v", err)
	}

	version, err := goose.GetDBVersion(db)
	if err != nil {
		db.Close()
		t.Fatalf("goose.GetDBVersion failed: %v", err)
	}
	if version != 9 {
		t.Errorf("Expected version 9 after upgrade, got %d", version)
	}

	// Verify existing data is preserved
	var taskCount, inboxCount, eventCount, convCount, msgCount, pendingCount int
	db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount)
	db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&inboxCount)
	db.QueryRow(`SELECT COUNT(*) FROM task_events`).Scan(&eventCount)
	db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&convCount)
	db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	db.QueryRow(`SELECT COUNT(*) FROM pending_actions`).Scan(&pendingCount)

	if taskCount != 3 {
		t.Errorf("Expected 3 tasks, got %d", taskCount)
	}
	if inboxCount != 2 {
		t.Errorf("Expected 2 inbox items, got %d", inboxCount)
	}
	if eventCount != 3 {
		t.Errorf("Expected 3 task events, got %d", eventCount)
	}
	if convCount != 1 {
		t.Errorf("Expected 1 conversation, got %d", convCount)
	}
	if msgCount != 1 {
		t.Errorf("Expected 1 message, got %d", msgCount)
	}
	if pendingCount != 1 {
		t.Errorf("Expected 1 pending action, got %d", pendingCount)
	}

	// Verify converted_task_id column exists
	var colExists int
	err = db.QueryRow(`SELECT 1 FROM pragma_table_info('inbox_items') WHERE name='converted_task_id'`).Scan(&colExists)
	if err != nil {
		t.Errorf("converted_task_id column missing: %v", err)
	}

	// Verify append-only triggers work
	_, err = db.Exec(`UPDATE task_events SET payload = '{}' WHERE task_id = 1`)
	if err == nil {
		t.Fatal("UPDATE on task_events should have been blocked after migration 009")
	}

	_, err = db.Exec(`DELETE FROM task_events WHERE task_id = 1`)
	if err == nil {
		t.Fatal("DELETE on task_events should have been blocked after migration 009")
	}

	// Verify task with history cannot be deleted
	_, err = db.Exec(`DELETE FROM tasks WHERE id = 1`)
	if err == nil {
		t.Fatal("DELETE task with history should have been blocked after migration 009")
	}

	// Verify task without history can be deleted
	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status) VALUES ('No History Task', 'low', 'open')`)
	_, err = db.Exec(`DELETE FROM tasks WHERE title = 'No History Task'`)
	if err != nil {
		t.Errorf("DELETE task without history should be allowed: %v", err)
	}

	db.Close()
}

// TestAllTimestampFieldsStorageClass tests that all timestamp fields use integer storage
func TestAllTimestampFieldsStorageClass(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert task with all timestamp fields
	_, _ = db.Exec(`INSERT INTO tasks (title, priority, status, due_at, completed_at, cancelled_at) 
		VALUES ('Test', 'medium', 'completed', 1700000000, 1700000001, 1700000002)`)

	timestampFields := []string{
		"created_at", "updated_at", "due_at", "completed_at", "cancelled_at",
	}

	for _, field := range timestampFields {
		var fieldType string
		err := db.QueryRow(fmt.Sprintf("SELECT typeof(%s) FROM tasks WHERE id = 1", field)).Scan(&fieldType)
		if err != nil {
			t.Fatalf("Query failed for %s: %v", field, err)
		}
		if fieldType != "integer" {
			t.Errorf("%s storage class = %q, want 'integer'", field, fieldType)
		}
	}

	// Test inbox_items timestamps
	_, _ = db.Exec(`INSERT INTO inbox_items (text, created_at, processed_at) VALUES ('Test', 1700000000, 1700000001)`)
	for _, field := range []string{"created_at", "processed_at"} {
		var fieldType string
		err := db.QueryRow(fmt.Sprintf("SELECT typeof(%s) FROM inbox_items WHERE id = 1", field)).Scan(&fieldType)
		if err != nil {
			t.Fatalf("Query failed for %s: %v", field, err)
		}
		if fieldType != "integer" {
			t.Errorf("inbox_items.%s storage class = %q, want 'integer'", field, fieldType)
		}
	}

	// Test conversations timestamps
	_, _ = db.Exec(`INSERT INTO conversations (title, created_at, updated_at, ended_at) VALUES ('Test', 1700000000, 1700000001, 1700000002)`)
	for _, field := range []string{"created_at", "updated_at", "ended_at"} {
		var fieldType string
		err := db.QueryRow(fmt.Sprintf("SELECT typeof(%s) FROM conversations WHERE id = 1", field)).Scan(&fieldType)
		if err != nil {
			t.Fatalf("Query failed for %s: %v", field, err)
		}
		if fieldType != "integer" {
			t.Errorf("conversations.%s storage class = %q, want 'integer'", field, fieldType)
		}
	}

	// Test messages timestamps
	_, _ = db.Exec(`INSERT INTO messages (conversation_id, role, content, created_at) VALUES (1, 'user', 'Test', 1700000000)`)
	var fieldType string
	err := db.QueryRow("SELECT typeof(created_at) FROM messages WHERE id = 1").Scan(&fieldType)
	if err != nil {
		t.Fatalf("Query failed for messages.created_at: %v", err)
	}
	if fieldType != "integer" {
		t.Errorf("messages.created_at storage class = %q, want 'integer'", fieldType)
	}

	// Test pending_actions timestamps
	_, _ = db.Exec(`INSERT INTO pending_actions (conversation_id, message_id, tool_name, arguments, preview, status, created_at, resolved_at) 
		VALUES (1, 1, 'test', '{}', 'Test', 'pending', 1700000000, 1700000001)`)
	for _, field := range []string{"created_at", "resolved_at"} {
		var fieldType string
		err := db.QueryRow(fmt.Sprintf("SELECT typeof(%s) FROM pending_actions WHERE id = 1", field)).Scan(&fieldType)
		if err != nil {
			t.Fatalf("Query failed for pending_actions.%s: %v", field, err)
		}
		if fieldType != "integer" {
			t.Errorf("pending_actions.%s storage class = %q, want 'integer'", field, fieldType)
		}
	}

	// Test task_events timestamps
	_, _ = db.Exec(`INSERT INTO task_events (task_id, type, payload, actor, created_at) VALUES (1, 'created', '{}', 'user', 1700000000)`)
	err = db.QueryRow("SELECT typeof(created_at) FROM task_events WHERE id = 1").Scan(&fieldType)
	if err != nil {
		t.Fatalf("Query failed for task_events.created_at: %v", err)
	}
	if fieldType != "integer" {
		t.Errorf("task_events.created_at storage class = %q, want 'integer'", fieldType)
	}
}
