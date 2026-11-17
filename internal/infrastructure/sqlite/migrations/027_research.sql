-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 7: researcher and external sources.
--
-- Reports keep the evidence as one JSON column: findings carry their citations
-- and the evidence is read as a whole, never row by row. Sources keep the
-- permission the user granted (read / write / sync) and the trust LifeOS gives
-- them when comparing claims. Watches keep a bounded baseline of page
-- fingerprints, which is what turns "the page changed" into a report instead of
-- a notification storm. Every outbound interaction is journalled, including the
-- refused ones, so an audit answer exists without replaying logs.

-- Web pages are a first-class provenance kind for the knowledge base: a fact
-- learned from the internet must be distinguishable from what the user said or
-- what a document contained. SQLite cannot widen a CHECK constraint in place,
-- so the table is rebuilt; nothing references memory_sources, which keeps the
-- rebuild local.
CREATE TABLE memory_sources_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('user','assistant','conversation','document','task','event','system','web')),
    source_id INTEGER NOT NULL DEFAULT 0,
    label TEXT NOT NULL,
    excerpt TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

INSERT INTO memory_sources_new (id, entry_id, kind, source_id, label, excerpt, observed_at, created_at)
SELECT id, entry_id, kind, source_id, label, excerpt, observed_at, created_at FROM memory_sources;

DROP TABLE memory_sources;
ALTER TABLE memory_sources_new RENAME TO memory_sources;

CREATE INDEX idx_memory_sources_entry ON memory_sources(entry_id, observed_at DESC, id DESC);
CREATE INDEX idx_memory_sources_origin ON memory_sources(kind, source_id, entry_id);

CREATE TABLE research_reports (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    question           TEXT NOT NULL,
    status             TEXT NOT NULL CHECK(status IN ('running','completed','failed')),
    queries_json       TEXT NOT NULL DEFAULT '[]',
    summary            TEXT NOT NULL DEFAULT '',
    findings_json      TEXT NOT NULL DEFAULT '[]',
    sources_count      INTEGER NOT NULL DEFAULT 0,
    knowledge_entry_id INTEGER REFERENCES memory_entries(id) ON DELETE SET NULL,
    error              TEXT NOT NULL DEFAULT '',
    started_at         INTEGER NOT NULL,
    finished_at        INTEGER,
    version            INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_research_reports_started ON research_reports(started_at DESC, id DESC);
CREATE INDEX idx_research_reports_status ON research_reports(status, started_at DESC);

CREATE TABLE research_connections (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK(kind IN ('web_search','http_api','mcp','script')),
    mode                 TEXT NOT NULL CHECK(mode IN ('read','write','sync')),
    trust                TEXT NOT NULL DEFAULT 'normal' CHECK(trust IN ('high','normal','low')),
    format               TEXT NOT NULL DEFAULT '',
    endpoint             TEXT NOT NULL,
    notes                TEXT NOT NULL DEFAULT '',
    enabled              INTEGER NOT NULL DEFAULT 0,
    created_by_assistant INTEGER NOT NULL DEFAULT 0,
    last_used_at         INTEGER,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    version              INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_research_connections_enabled ON research_connections(enabled, kind);

CREATE TABLE research_watches (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    topic               TEXT NOT NULL,
    query               TEXT NOT NULL,
    queries_json        TEXT NOT NULL DEFAULT '[]',
    cadence             TEXT NOT NULL CHECK(cadence IN ('daily','weekly','manual')),
    enabled             INTEGER NOT NULL DEFAULT 1,
    next_check_at       INTEGER,
    last_checked_at     INTEGER,
    last_change_at      INTEGER,
    last_change_summary TEXT NOT NULL DEFAULT '',
    seen_json           TEXT NOT NULL DEFAULT '[]',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    version             INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_research_watches_due ON research_watches(enabled, next_check_at);

CREATE TABLE research_calls (
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

CREATE INDEX idx_research_calls_connection ON research_calls(connection_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS research_calls;
DROP TABLE IF EXISTS research_watches;
DROP TABLE IF EXISTS research_connections;
DROP TABLE IF EXISTS research_reports;

-- The previous provenance set did not include 'web'. Rows of that kind are
-- removed before the constraint is restored, so the rollback cannot fail on
-- its own data.
CREATE TABLE memory_sources_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('user','assistant','conversation','document','task','event','system')),
    source_id INTEGER NOT NULL DEFAULT 0,
    label TEXT NOT NULL,
    excerpt TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

INSERT INTO memory_sources_old (id, entry_id, kind, source_id, label, excerpt, observed_at, created_at)
SELECT id, entry_id, kind, source_id, label, excerpt, observed_at, created_at
FROM memory_sources WHERE kind <> 'web';

DROP TABLE memory_sources;
ALTER TABLE memory_sources_old RENAME TO memory_sources;

CREATE INDEX idx_memory_sources_entry ON memory_sources(entry_id, observed_at DESC, id DESC);
CREATE INDEX idx_memory_sources_origin ON memory_sources(kind, source_id, entry_id);

-- +goose StatementEnd
