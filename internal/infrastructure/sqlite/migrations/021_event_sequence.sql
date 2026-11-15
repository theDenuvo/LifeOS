-- +goose Up
-- +goose StatementBegin

-- Event ids must stay unique for the life of the installation, not just for the
-- life of the process: a browser that reconnects after a restart sends the last
-- id it saw, and a counter starting from zero would make every new event look
-- older than the client's position and be dropped.
--
-- The counter is reserved in blocks, so a normal run costs one write at startup
-- while a crash still cannot reuse an id that was already handed out.
CREATE TABLE event_sequence (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value INTEGER NOT NULL CHECK (value >= 0)
);

INSERT INTO event_sequence (id, value) VALUES (1, 0);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_sequence;
-- +goose StatementEnd
