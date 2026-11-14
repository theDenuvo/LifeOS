-- +goose Up
-- +goose StatementBegin

CREATE TABLE documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL,
    mime TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    path TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','processing','processed','failed')) DEFAULT 'pending',
    error TEXT,
    extracted_text TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX idx_documents_status ON documents(status, created_at);

CREATE TABLE document_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    note TEXT,
    due_date DATE,
    priority TEXT NOT NULL CHECK(priority IN ('low','medium','high')) DEFAULT 'medium',
    confidence REAL NOT NULL DEFAULT 0,
    source TEXT,
    status TEXT NOT NULL CHECK(status IN ('proposed','accepted','dismissed')) DEFAULT 'proposed',
    task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX idx_document_tasks_document ON document_tasks(document_id, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS document_tasks;
DROP TABLE IF EXISTS documents;
-- +goose StatementEnd
