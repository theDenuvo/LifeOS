-- +goose Up
-- +goose StatementBegin

-- Repair pass for migration 024: databases that already ran the original 024
-- kept its weaker triggers (a source pointing at a non-existent conversation
-- made the kind subquery return NULL, so the check silently passed), and
-- no data cleanup ran at all. Migrations are immutable, so the corrections
-- live here and re-apply them over whatever state 024 left behind.

-- Legacy rows may carry routes written before any guard existed (from a
-- directly opened project chat) or slipped through the NULL hole in the
-- original 024 triggers (source pointing at a conversation that no longer
-- exists). They are cleared rather than rejected: a route is re-bound
-- automatically on the next delegation from the main chat, so clearing is
-- safe and self-healing, while a failed migration would leave the system
-- unusable.
UPDATE conversations
SET source_conversation_id = NULL
WHERE source_conversation_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM conversations c
      WHERE c.id = conversations.source_conversation_id
        AND c.kind = 'main'
  );

DROP TRIGGER IF EXISTS conversations_source_must_be_main_insert;
DROP TRIGGER IF EXISTS conversations_source_must_be_main_update;

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

-- Restore the original 024 triggers so a downgrade returns to the state
-- migration 024 left behind.
CREATE TRIGGER conversations_source_must_be_main_insert
BEFORE INSERT ON conversations
FOR EACH ROW
WHEN NEW.source_conversation_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'workspace source must be a main conversation')
    WHERE (SELECT kind FROM conversations WHERE id = NEW.source_conversation_id) <> 'main';
END;

CREATE TRIGGER conversations_source_must_be_main_update
BEFORE UPDATE OF source_conversation_id ON conversations
FOR EACH ROW
WHEN NEW.source_conversation_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'workspace source must be a main conversation')
    WHERE (SELECT kind FROM conversations WHERE id = NEW.source_conversation_id) <> 'main';
END;

-- +goose StatementEnd
