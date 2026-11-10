-- +goose Up
-- +goose StatementBegin

CREATE TABLE pending_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    arguments TEXT NOT NULL, -- JSON
    preview TEXT NOT NULL, -- JSON
    status TEXT NOT NULL CHECK(status IN ('pending','confirmed','rejected','expired')) DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    resolved_at TIMESTAMP,
    resolution TEXT CHECK(resolution IN ('confirmed','rejected','expired'))
);

CREATE INDEX idx_pending_actions_conversation ON pending_actions(conversation_id, status);
CREATE INDEX idx_pending_actions_status ON pending_actions(status);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS pending_actions;