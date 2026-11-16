-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 3: a workspace's route back must point at a main chat. A
-- project conversation must never become the delivery target of
-- report_result, so the database itself refuses any source that is not a
-- main conversation.
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

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS conversations_source_must_be_main_update;
DROP TRIGGER IF EXISTS conversations_source_must_be_main_insert;

-- +goose StatementEnd
