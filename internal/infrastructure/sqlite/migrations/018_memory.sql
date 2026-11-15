-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 2: durable memory, reusable knowledge and inspectable
-- provenance. Superseded claims are retained so corrections remain auditable.
CREATE TABLE memory_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    area TEXT NOT NULL CHECK(area IN ('personal','knowledge')),
    category TEXT NOT NULL,
    title TEXT NOT NULL,
    normalized_title TEXT NOT NULL,
    content TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    document_sha TEXT,
    certainty TEXT NOT NULL CHECK(certainty IN ('fact','assumption','inference')),
    confidence REAL NOT NULL CHECK(confidence >= 0 AND confidence <= 1),
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','disputed','superseded')),
    superseded_by INTEGER REFERENCES memory_entries(id) ON DELETE SET NULL,
    entities TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK(version >= 1)
);

CREATE INDEX idx_memory_list ON memory_entries(area, status, updated_at DESC, id DESC);
CREATE INDEX idx_memory_subject ON memory_entries(area, category, normalized_title, status, updated_at DESC);
-- An exact fact may exist only once outside the version history, so two
-- concurrent learning runs cannot both insert the same claim.
CREATE UNIQUE INDEX idx_memory_dedupe_active ON memory_entries(dedupe_key) WHERE status <> 'superseded';
CREATE INDEX idx_memory_superseded_by ON memory_entries(superseded_by);

CREATE TABLE memory_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('user','assistant','conversation','document','task','event','system')),
    source_id INTEGER NOT NULL DEFAULT 0,
    label TEXT NOT NULL,
    excerpt TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_memory_sources_entry ON memory_sources(entry_id, observed_at DESC, id DESC);
CREATE INDEX idx_memory_sources_origin ON memory_sources(kind, source_id, entry_id);

CREATE TABLE memory_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK(target_type IN ('memory','task','event','conversation','document','inbox','goal')),
    target_id INTEGER NOT NULL,
    relation TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE(entry_id, target_type, target_id, relation)
);

CREATE INDEX idx_memory_links_entry ON memory_links(entry_id, id);
CREATE INDEX idx_memory_links_target ON memory_links(target_type, target_id, entry_id);

CREATE TABLE memory_conflicts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    existing_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    candidate_id INTEGER NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','resolved')),
    resolution TEXT,
    created_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP
);

CREATE INDEX idx_memory_conflicts_status ON memory_conflicts(status, created_at DESC, id DESC);
CREATE UNIQUE INDEX idx_memory_conflict_pair_pending
    ON memory_conflicts(existing_id, candidate_id) WHERE status = 'pending';

CREATE VIRTUAL TABLE memory_fts USING fts5(
    title,
    content,
    entities,
    content='memory_entries',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER memory_fts_insert AFTER INSERT ON memory_entries BEGIN
    INSERT INTO memory_fts(rowid, title, content, entities)
    VALUES (new.id, new.title, new.content, new.entities);
END;

CREATE TRIGGER memory_fts_delete AFTER DELETE ON memory_entries BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, content, entities)
    VALUES ('delete', old.id, old.title, old.content, old.entities);
END;

CREATE TRIGGER memory_fts_update AFTER UPDATE ON memory_entries BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, content, entities)
    VALUES ('delete', old.id, old.title, old.content, old.entities);
    INSERT INTO memory_fts(rowid, title, content, entities)
    VALUES (new.id, new.title, new.content, new.entities);
END;

INSERT INTO memory_fts(rowid, title, content, entities)
SELECT id, title, content, entities FROM memory_entries;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS memory_fts_update;
DROP TRIGGER IF EXISTS memory_fts_delete;
DROP TRIGGER IF EXISTS memory_fts_insert;
DROP TABLE IF EXISTS memory_fts;
DROP TABLE IF EXISTS memory_conflicts;
DROP TABLE IF EXISTS memory_links;
DROP TABLE IF EXISTS memory_sources;
DROP TABLE IF EXISTS memory_entries;

-- +goose StatementEnd
