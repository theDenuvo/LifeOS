-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 8: skills and assistant self-improvement. A skill is a
-- versioned instruction (when it applies, steps, limits, examples); the
-- current text lives on the skill row and every change appends a version,
-- so a rollback is a new version and history is never rewritten.
-- Improvement proposals keep the found problem, evidence, the exact
-- proposed change, its expected effect and the pre-decision test result;
-- nothing changes until the user applies one. Feedback and the usage
-- journal are the evidence later analysis reads — a judgement alone never
-- rewrites an active instruction.

CREATE TABLE skills (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT NOT NULL,
    normalized_name      TEXT NOT NULL UNIQUE,
    purpose              TEXT NOT NULL,
    when_to_use          TEXT NOT NULL,
    instructions         TEXT NOT NULL,
    limitations          TEXT NOT NULL DEFAULT '',
    examples             TEXT NOT NULL DEFAULT '',
    tags_json            TEXT NOT NULL DEFAULT '[]',
    status               TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','active','disabled','archived')),
    current_version      INTEGER NOT NULL DEFAULT 1,
    source               TEXT NOT NULL DEFAULT 'user' CHECK(source IN ('user','assistant','system')),
    created_by_assistant INTEGER NOT NULL DEFAULT 0,
    use_count            INTEGER NOT NULL DEFAULT 0,
    good_count           INTEGER NOT NULL DEFAULT 0,
    bad_count            INTEGER NOT NULL DEFAULT 0,
    last_used_at         INTEGER,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    version              INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_skills_status ON skills(status, updated_at DESC, id DESC);

CREATE TABLE skill_versions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id     INTEGER NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no   INTEGER NOT NULL,
    name         TEXT NOT NULL,
    purpose      TEXT NOT NULL,
    when_to_use  TEXT NOT NULL,
    instructions TEXT NOT NULL,
    limitations  TEXT NOT NULL DEFAULT '',
    examples     TEXT NOT NULL DEFAULT '',
    tags_json    TEXT NOT NULL DEFAULT '[]',
    change_note  TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT 'user' CHECK(created_by IN ('user','assistant','system')),
    created_at   INTEGER NOT NULL,
    UNIQUE(skill_id, version_no)
);

CREATE INDEX idx_skill_versions_skill ON skill_versions(skill_id, version_no DESC);

CREATE TABLE skill_proposals (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id            INTEGER REFERENCES skills(id) ON DELETE CASCADE,
    kind                TEXT NOT NULL CHECK(kind IN ('skill_version','skill_create','working_instructions')),
    status              TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed','applied','rejected')),
    problem             TEXT NOT NULL,
    evidence_json       TEXT NOT NULL DEFAULT '[]',
    proposed_name       TEXT NOT NULL DEFAULT '',
    proposed_purpose    TEXT NOT NULL DEFAULT '',
    proposed_when       TEXT NOT NULL DEFAULT '',
    proposed_instructions TEXT NOT NULL DEFAULT '',
    proposed_limitations TEXT NOT NULL DEFAULT '',
    proposed_examples   TEXT NOT NULL DEFAULT '',
    proposed_tags_json  TEXT NOT NULL DEFAULT '[]',
    expected_effect     TEXT NOT NULL,
    test_scenarios_json TEXT NOT NULL DEFAULT '[]',
    test_result         TEXT NOT NULL DEFAULT '',
    base_version        INTEGER,
    conversation_id     INTEGER,
    result_skill_id     INTEGER REFERENCES skills(id) ON DELETE SET NULL,
    created_at          INTEGER NOT NULL,
    resolved_at         INTEGER,
    version             INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_skill_proposals_skill ON skill_proposals(skill_id, status, created_at DESC);
CREATE INDEX idx_skill_proposals_status ON skill_proposals(status, created_at DESC);

CREATE TABLE skill_feedback (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id        INTEGER NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no      INTEGER NOT NULL,
    rating          TEXT NOT NULL CHECK(rating IN ('good','bad')),
    comment         TEXT NOT NULL DEFAULT '',
    conversation_id INTEGER,
    created_at      INTEGER NOT NULL
);

CREATE INDEX idx_skill_feedback_skill ON skill_feedback(skill_id, created_at DESC, id DESC);

CREATE TABLE skill_usages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id        INTEGER NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no      INTEGER NOT NULL,
    conversation_id INTEGER,
    context         TEXT NOT NULL DEFAULT '',
    matched_json    TEXT NOT NULL DEFAULT '[]',
    created_at      INTEGER NOT NULL
);

CREATE INDEX idx_skill_usages_skill ON skill_usages(skill_id, created_at DESC, id DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS skill_usages;
DROP TABLE IF EXISTS skill_feedback;
DROP TABLE IF EXISTS skill_proposals;
DROP TABLE IF EXISTS skill_versions;
DROP TABLE IF EXISTS skills;

-- +goose StatementEnd
