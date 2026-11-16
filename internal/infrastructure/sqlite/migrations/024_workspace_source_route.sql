-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 3: a workspace's route back must point at a main chat. A
-- project conversation must never become the delivery target of
-- report_result.

-- Legacy rows may carry routes written before the tool guard existed (from a
-- directly opened project chat, or to a conversation that no longer exists).
-- They are cleared rather than rejected: a route is re-bound automatically on
-- the next delegation from the main chat, so clearing is safe and
-- self-healing, while a failed migration would leave the system unusable.
-- Runs before the triggers, so the cleanup itself is not subject to them.
UPDATE conversations
SET source_conversation_id = NULL
WHERE source_conversation_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM conversations c
      WHERE c.id = conversations.source_conversation_id
        AND c.kind = 'main'
  );

CREATE TRIGGER conversations_source_must_be_main_insert
BEFORE INSERT ON conversations
FOR EACH ROW
WHEN NEW.source_conversation_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'workspace source must be an existing main conversation')
    WHERE NOT EXISTS (
        SELECT 1 FROM conversations
        WHERE id = NEW.source_conversation_id
          AND kind = 'main'
    );
END;

CREATE TRIGGER conversations_source_must_be_main_update
BEFORE UPDATE OF source_conversation_id ON conversations
FOR EACH ROW
WHEN NEW.source_conversation_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'workspace source must be an existing main conversation')
    WHERE NOT EXISTS (
        SELECT 1 FROM conversations
        WHERE id = NEW.source_conversation_id
          AND kind = 'main'
    );
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS conversations_source_must_be_main_update;
DROP TRIGGER IF EXISTS conversations_source_must_be_main_insert;

-- +goose StatementEnd
