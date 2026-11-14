-- +goose Up
-- +goose StatementBegin

-- Structured attachments extracted from tool results, stored as a JSON array.
-- Older messages keep NULL; clients fall back to parsing tool output until a
-- conversation is regenerated.
ALTER TABLE messages ADD COLUMN attachments TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN attachments;
-- +goose StatementEnd
