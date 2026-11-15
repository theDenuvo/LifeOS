-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 3: the main chat and project workspaces. Every existing
-- conversation stays a main-chat dialogue; project spaces are conversations
-- marked with kind='project' and carrying their own purpose.
ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'main';
ALTER TABLE conversations ADD COLUMN purpose TEXT;
ALTER TABLE conversations ADD COLUMN source_conversation_id INTEGER;

CREATE INDEX idx_conversations_kind ON conversations(kind, updated_at DESC, id DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_conversations_kind;
ALTER TABLE conversations DROP COLUMN source_conversation_id;
ALTER TABLE conversations DROP COLUMN purpose;
ALTER TABLE conversations DROP COLUMN kind;

-- +goose StatementEnd
