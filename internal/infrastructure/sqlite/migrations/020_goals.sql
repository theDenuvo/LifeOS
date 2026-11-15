-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 4: long-term goals and initiative management. A goal starts
-- proposed and becomes active only after user approval; milestone states,
-- links to other LifeOS objects and the progress journal provide the factual
-- evidence; proposals keep significant plan changes in a reviewable queue.

CREATE TABLE goals (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed','active','paused','achieved','abandoned')),
    target_date  INTEGER,
    approved_at  INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_goals_status ON goals(status, updated_at DESC);

CREATE TABLE goal_milestones (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id      INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL DEFAULT 0,
    title        TEXT NOT NULL,
    criteria     TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_progress','done','skipped')),
    due_date     INTEGER,
    completed_at INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_goal_milestones_goal ON goal_milestones(goal_id, position);

CREATE TABLE goal_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id     INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK(target_type IN ('conversation','task','event','document','memory','inbox')),
    target_id   INTEGER NOT NULL,
    relation    TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE(goal_id, target_type, target_id, relation)
);

CREATE INDEX idx_goal_links_target ON goal_links(target_type, target_id);

CREATE TABLE goal_progress (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id     INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK(kind IN ('goal_created','approved','milestone','task_done','note','status','proposal')),
    summary     TEXT NOT NULL,
    source_type TEXT,
    source_id   INTEGER,
    recorded_at INTEGER NOT NULL
);

CREATE INDEX idx_goal_progress_goal ON goal_progress(goal_id, recorded_at DESC);

CREATE TABLE goal_proposals (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id    INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    summary    TEXT NOT NULL,
    payload    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applied','rejected')),
    created_at INTEGER NOT NULL,
    decided_at INTEGER
);

CREATE INDEX idx_goal_proposals_goal ON goal_proposals(goal_id, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS goal_proposals;
DROP TABLE IF EXISTS goal_progress;
DROP TABLE IF EXISTS goal_links;
DROP TABLE IF EXISTS goal_milestones;
DROP TABLE IF EXISTS goals;

-- +goose StatementEnd
