-- +goose Up
-- +goose StatementBegin

-- Review fixes for roadmap stage 1.
--
-- A reminder is now delivered in two visible steps: the moment it becomes due it
-- is offered to the client (durably, from the database), and only after the
-- client actually shows it is it acknowledged. notified_at records that the live
-- event was published once, so a disconnected client does not cause the same
-- event to be published again on every tick, while the reminder itself stays
-- available for a client that reconnects later.
ALTER TABLE task_reminders ADD COLUMN notified_at TIMESTAMP;

CREATE INDEX idx_task_reminders_actionable ON task_reminders(status, fire_at);

-- completion_checks keeps the reason behind every conclusion about a finished
-- task: which sources were consulted, what they returned and what the user
-- decided. Deciding "done" without keeping the grounds would make the conclusion
-- unauditable, and a wrong conclusion impossible to review.
CREATE TABLE completion_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    verdict TEXT NOT NULL CHECK(verdict IN ('confirmed','insufficient','contradictory','already_completed')),
    confidence REAL NOT NULL DEFAULT 0 CHECK(confidence >= 0 AND confidence <= 1),
    reason TEXT NOT NULL,
    evidence TEXT,
    sources TEXT,
    decision TEXT NOT NULL DEFAULT 'none' CHECK(decision IN ('none','accepted','declined')),
    decided_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX idx_completion_checks_task ON completion_checks(task_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS completion_checks;
DROP INDEX IF EXISTS idx_task_reminders_actionable;
ALTER TABLE task_reminders DROP COLUMN notified_at;

-- +goose StatementEnd
