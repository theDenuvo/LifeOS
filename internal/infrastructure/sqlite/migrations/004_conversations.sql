-- +goose Up
-- +goose StatementBegin

CREATE TABLE conversations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT,
    context_screen TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    ended_at TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS conversations;