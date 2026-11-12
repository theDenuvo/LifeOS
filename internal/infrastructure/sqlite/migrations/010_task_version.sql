-- +goose Up
-- +goose StatementBegin

-- Optimistic locking metadata for tasks.
-- The Application layer already returns a version with every task and requires
-- conditional writes (WHERE id = ? AND version = ?). The column is introduced
-- here, together with the persistence implementation that uses it.
ALTER TABLE tasks ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK(version >= 1);

-- Application timestamps are authoritative: one clock reading stamps the task
-- and its audit event. The SQL trigger from 008 overwrote tasks.updated_at with
-- the database clock on every UPDATE, which broke that guarantee (and made the
-- audit trail disagree with the row). The repository now always writes
-- updated_at explicitly, so the trigger is removed.
DROP TRIGGER IF EXISTS tasks_updated_at;

-- Supports the optimistic-lock lookup and duplicate detection by title in
-- list screens without scanning the whole table.
CREATE INDEX idx_tasks_updated_at ON tasks(updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_tasks_updated_at;

CREATE TRIGGER tasks_updated_at
AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%s','now') WHERE id = NEW.id;
END;

ALTER TABLE tasks DROP COLUMN version;

-- +goose StatementEnd
