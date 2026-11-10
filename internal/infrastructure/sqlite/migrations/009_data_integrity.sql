-- +goose Up
-- +goose StatementBegin

-- 1. Add converted_task_id to inbox_items for tracking conversion
ALTER TABLE inbox_items ADD COLUMN converted_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL;
CREATE INDEX idx_inbox_converted_task ON inbox_items(converted_task_id);

-- 2. Add restored to task_events type CHECK constraint (already exists in 003, but ensuring)
-- The type CHECK already includes 'restored' from migration 003, no change needed

-- 3. Protect task_events from UPDATE and DELETE (append-only enforcement)
CREATE TRIGGER task_events_no_update
BEFORE UPDATE ON task_events
BEGIN
    SELECT RAISE(ABORT, 'task_events is append-only: UPDATE not allowed');
END;

CREATE TRIGGER task_events_no_delete
BEFORE DELETE ON task_events
BEGIN
    SELECT RAISE(ABORT, 'task_events is append-only: DELETE not allowed');
END;

-- 4. Protect tasks from physical DELETE if they have task_events history
CREATE TRIGGER tasks_no_delete_with_history
BEFORE DELETE ON tasks
WHEN EXISTS (SELECT 1 FROM task_events WHERE task_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'Cannot delete task with history: use status=cancelled instead');
END;

-- 5. Add restored event type to task_events if not present (already in 003)
-- The CHECK constraint in 003 already includes 'restored', verified

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS tasks_no_delete_with_history;
DROP TRIGGER IF EXISTS task_events_no_delete;
DROP TRIGGER IF EXISTS task_events_no_update;

DROP INDEX IF EXISTS idx_inbox_converted_task;
ALTER TABLE inbox_items DROP COLUMN converted_task_id;

-- +goose StatementEnd