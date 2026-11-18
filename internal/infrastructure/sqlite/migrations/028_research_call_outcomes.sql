-- The journal could not tell a read from a change, and its outcome set had no
-- room for a call whose result is not known yet.
--
-- A changing call is now journalled before it leaves ("started"), closed with
-- what happened ("ok", "refused", "failed"), and reported as "unknown" if
-- LifeOS was restarted while it was in flight. The HTTP method is recorded
-- separately from the operation, because for an MCP source the operation is a
-- tool name and says nothing about the method.

-- +goose Up
-- +goose StatementBegin

CREATE TABLE research_calls_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_id INTEGER NOT NULL DEFAULT 0,
    name          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    mode          TEXT NOT NULL,
    method        TEXT NOT NULL DEFAULT '',
    operation     TEXT NOT NULL DEFAULT '',
    outcome       TEXT NOT NULL CHECK(outcome IN ('started','ok','refused','failed','unknown')),
    detail        TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

INSERT INTO research_calls_new
    (id, connection_id, name, kind, mode, method, operation, outcome, detail, duration_ms, created_at)
SELECT id, connection_id, name, kind, mode, '', operation, outcome, detail, duration_ms, created_at
FROM research_calls;

DROP TABLE research_calls;
ALTER TABLE research_calls_new RENAME TO research_calls;

CREATE INDEX idx_research_calls_connection ON research_calls(connection_id, created_at DESC);
CREATE INDEX idx_research_calls_outcome ON research_calls(outcome);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE research_calls_old (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_id INTEGER NOT NULL DEFAULT 0,
    name          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    mode          TEXT NOT NULL,
    operation     TEXT NOT NULL DEFAULT '',
    outcome       TEXT NOT NULL CHECK(outcome IN ('ok','refused','failed')),
    detail        TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

-- Rows of the new kinds are dropped before the older constraint is restored:
-- a rollback must not fail on its own data.
INSERT INTO research_calls_old
    (id, connection_id, name, kind, mode, operation, outcome, detail, duration_ms, created_at)
SELECT id, connection_id, name, kind, mode, operation, outcome, detail, duration_ms, created_at
FROM research_calls
WHERE outcome IN ('ok','refused','failed');

DROP TABLE research_calls;
ALTER TABLE research_calls_old RENAME TO research_calls;

CREATE INDEX idx_research_calls_connection ON research_calls(connection_id, created_at DESC);

-- +goose StatementEnd
