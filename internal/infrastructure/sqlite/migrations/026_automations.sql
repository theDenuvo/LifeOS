-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 6: user automations. A drafted scenario waits for user
-- approval; the trigger (schedule / event / condition) lives in a JSON column
-- because the three arms share no shape and the runner reads the whole
-- aggregate at once. The run journal is append-only per run: steps are
-- recorded with their output or error when the run finishes.

CREATE TABLE automations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed','active','paused','archived')),
    trigger_kind    TEXT NOT NULL CHECK(trigger_kind IN ('schedule','event','condition')),
    trigger_json    TEXT NOT NULL,
    steps_json      TEXT NOT NULL,
    expected_result TEXT NOT NULL DEFAULT '',
    consequences    TEXT NOT NULL DEFAULT '',
    approved_at     INTEGER,
    last_run_at     INTEGER,
    next_run_at     INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    version         INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_automations_status ON automations(status, updated_at DESC);
CREATE INDEX idx_automations_schedule ON automations(trigger_kind, next_run_at) WHERE status = 'active';

CREATE TABLE automation_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    automation_id INTEGER NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    trigger       TEXT NOT NULL CHECK(trigger IN ('schedule','event','condition','manual')),
    status        TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running','succeeded','failed','skipped')),
    steps_json    TEXT NOT NULL DEFAULT '[]',
    summary       TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER
);

CREATE INDEX idx_automation_runs_automation ON automation_runs(automation_id, started_at DESC);
CREATE INDEX idx_automation_runs_running ON automation_runs(automation_id) WHERE status = 'running';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS automation_runs;
DROP TABLE IF EXISTS automations;

-- +goose StatementEnd
