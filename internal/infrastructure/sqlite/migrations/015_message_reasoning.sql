-- +goose Up
-- +goose StatementBegin

-- Optional model reasoning trace, shown as a collapsible block in the chat.
ALTER TABLE messages ADD COLUMN reasoning TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN reasoning;
-- +goose StatementEnd
