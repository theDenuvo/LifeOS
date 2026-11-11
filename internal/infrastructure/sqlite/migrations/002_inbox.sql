-- +goose Up
-- +goose StatementBegin

CREATE TABLE inbox_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    text TEXT NOT NULL CHECK(length(text) > 0 AND length(text) <= 2000),
    meta TEXT, -- JSON
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    processed_at TIMESTAMP
);

CREATE INDEX idx_inbox_created_at ON inbox_items(created_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS inbox_items;