-- +goose Up
-- +goose StatementBegin

CREATE TABLE tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL CHECK(length(title) > 0 AND length(title) <= 500),
    note TEXT CHECK(length(note) <= 5000),
    priority TEXT NOT NULL CHECK(priority IN ('low','medium','high')) DEFAULT 'medium',
    status TEXT NOT NULL CHECK(status IN ('open','completed','cancelled')) DEFAULT 'open',
    planned_date DATE,
    due_date DATE,
    due_at TIMESTAMP,
    timezone TEXT NOT NULL DEFAULT 'Europe/Moscow',
    tags TEXT, -- JSON array of strings
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    completed_at TIMESTAMP,
    cancelled_at TIMESTAMP,
    parent_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_priority ON tasks(priority);
CREATE INDEX idx_tasks_planned_date ON tasks(planned_date);
CREATE INDEX idx_tasks_due_date ON tasks(due_date);
CREATE INDEX idx_tasks_due_at ON tasks(due_at);
CREATE INDEX idx_tasks_created_at ON tasks(created_at);
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS tasks;