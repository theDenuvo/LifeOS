-- +goose Up
-- +goose StatementBegin

CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('user','assistant','tool')),
    content TEXT NOT NULL,
    tool_calls TEXT, -- JSON
    tool_results TEXT, -- JSON
    metadata TEXT, -- JSON
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS messages;