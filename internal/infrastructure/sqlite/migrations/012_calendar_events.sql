-- +goose Up
-- +goose StatementBegin

-- Calendar events are meetings/appointments. Tasks stay in the tasks table;
-- the calendar read model combines both without duplicating task data here.
CREATE TABLE calendar_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL CHECK(length(title) > 0 AND length(title) <= 200),
    description TEXT CHECK(description IS NULL OR length(description) <= 2000),
    location TEXT CHECK(location IS NULL OR length(location) <= 200),
    calendar TEXT NOT NULL CHECK(calendar IN ('work','meetings','personal')) DEFAULT 'personal',
    starts_at TIMESTAMP NOT NULL,
    ends_at TIMESTAMP NOT NULL,
    all_day INTEGER NOT NULL DEFAULT 0 CHECK(all_day IN (0,1)),
    timezone TEXT NOT NULL DEFAULT 'Europe/Moscow',
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    version INTEGER NOT NULL DEFAULT 1,
    CHECK(ends_at > starts_at)
);

CREATE INDEX idx_calendar_events_range ON calendar_events(starts_at, ends_at);
CREATE INDEX idx_calendar_events_calendar ON calendar_events(calendar, starts_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS calendar_events;
-- +goose StatementEnd
