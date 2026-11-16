-- +goose Up
-- +goose StatementBegin

-- Roadmap stage 5: full proactivity. Insights are still derived, but when
-- LifeOS itself starts the dialogue, the contact is stored so the reason, the
-- proposal and the user's reaction can be reviewed and learned from.
CREATE TABLE outreach (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL,
    message_id      INTEGER NOT NULL,
    insight_key     TEXT NOT NULL,
    kind            TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK(severity IN ('critical','warning','info')),
    title           TEXT NOT NULL,
    reason          TEXT NOT NULL,
    proposal        TEXT NOT NULL DEFAULT '',
    outcome         TEXT NOT NULL DEFAULT 'pending' CHECK(outcome IN ('pending','acknowledged','dismissed','snoozed')),
    created_at      INTEGER NOT NULL,
    decided_at      INTEGER,
    snoozed_until   INTEGER
);

CREATE INDEX idx_outreach_created ON outreach(created_at);
CREATE INDEX idx_outreach_outcome ON outreach(outcome, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outreach;
-- +goose StatementEnd
