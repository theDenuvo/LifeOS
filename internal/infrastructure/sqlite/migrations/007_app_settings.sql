-- +goose Up
-- +goose StatementBegin

CREATE TABLE app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL, -- JSON
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS app_settings;