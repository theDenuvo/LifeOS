-- +goose Up
-- +goose StatementBegin

-- Trigger for tasks.updated_at
CREATE TRIGGER tasks_updated_at
AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%s','now') WHERE id = NEW.id;
END;

-- Trigger for conversations.updated_at
CREATE TRIGGER conversations_updated_at
AFTER UPDATE ON conversations
BEGIN
    UPDATE conversations SET updated_at = strftime('%s','now') WHERE id = NEW.id;
END;

-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS tasks_updated_at;
DROP TRIGGER IF EXISTS conversations_updated_at;