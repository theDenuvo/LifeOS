-- +goose Up
-- +goose StatementBegin

-- Per-insight user state. Insights themselves are derived from live data, so
-- only dismissals and snoozes need to be stored. A stable key identifies the
-- detector (for example "overdue" or "inbox-backlog").
CREATE TABLE insight_states (
    key TEXT PRIMARY KEY,
    dismissed_at TIMESTAMP,
    snoozed_until TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS insight_states;
-- +goose StatementEnd
