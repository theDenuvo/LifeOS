-- +goose Up
-- +goose StatementBegin

-- The epoch identifies a concrete state of the event id counter. A browser
-- stores ids with the epoch that issued them, so after a database is restored
-- from an older copy the epoch no longer matches and the client is told to
-- resynchronise instead of trusting numbers that skip a lost window.
ALTER TABLE event_sequence ADD COLUMN epoch INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite has no DROP COLUMN in older versions; the table is recreated by the
-- 021 migration's Down, so this is only for completeness.
-- +goose StatementEnd
