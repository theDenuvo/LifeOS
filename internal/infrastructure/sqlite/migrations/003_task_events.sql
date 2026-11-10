-- +goose Up
-- +goose StatementBegin

CREATE TABLE task_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK(type IN (
        'created','updated','completed','cancelled','restored',
        'rescheduled','linked','converted_from_inbox'
    )),
    payload TEXT NOT NULL, -- JSON
    actor TEXT NOT NULL DEFAULT 'user' CHECK(actor IN ('user','assistant','system')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX idx_task_events_task_id ON task_events(task_id, created_at);
CREATE INDEX idx_task_events_type ON task_events(type);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS task_events;