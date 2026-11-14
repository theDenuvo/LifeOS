-- +goose Up
-- +goose StatementBegin

-- Stage 1 of the roadmap: time management.
--
-- estimate_minutes is how long the work takes, which is what makes planning
-- possible. expected_result is what "done" means for the task, so completion can
-- be checked against a stated outcome instead of a guess.
ALTER TABLE tasks ADD COLUMN estimate_minutes INTEGER
    CHECK(estimate_minutes IS NULL OR (estimate_minutes >= 5 AND estimate_minutes <= 1440));
ALTER TABLE tasks ADD COLUMN expected_result TEXT
    CHECK(expected_result IS NULL OR length(expected_result) <= 500);

-- repeat holds the recurrence rule as JSON:
-- {"freq":"daily|weekly|monthly","interval":1,"weekdays":[1,3,5],"until":"YYYY-MM-DD","window_days":1}
-- A recurring task is a template: completing it creates the next occurrence and
-- keeps this one in the history.
ALTER TABLE tasks ADD COLUMN repeat TEXT
    CHECK(repeat IS NULL OR length(repeat) <= 500);
ALTER TABLE tasks ADD COLUMN repeat_parent_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL;

CREATE INDEX idx_tasks_repeat_parent ON tasks(repeat_parent_id);

-- task_blocks are the time the user actually spends on a task. They are kept
-- apart from calendar_events: an event is an appointment with other people, a
-- block is the user's own working time and can be moved by the planner.
CREATE TABLE task_blocks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    starts_at TIMESTAMP NOT NULL,
    ends_at TIMESTAMP NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('planned','active','done','skipped')) DEFAULT 'planned',
    source TEXT NOT NULL CHECK(source IN ('auto','manual','assistant')) DEFAULT 'auto',
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    version INTEGER NOT NULL DEFAULT 1 CHECK(version >= 1),
    CHECK(ends_at > starts_at)
);

CREATE INDEX idx_task_blocks_range ON task_blocks(starts_at, ends_at);
CREATE INDEX idx_task_blocks_task ON task_blocks(task_id, starts_at);

-- task_reminders are the concrete moments at which the user should be reminded
-- about a task. They are derived from the deadline, priority and estimate, and
-- are regenerated whenever those change.
CREATE TABLE task_reminders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    fire_at TIMESTAMP NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('lead','due','overdue')) DEFAULT 'lead',
    status TEXT NOT NULL CHECK(status IN ('pending','sent','dismissed')) DEFAULT 'pending',
    sent_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%s','now')),
    CHECK(kind <> 'lead' OR status <> 'sent' OR sent_at IS NOT NULL)
);

CREATE UNIQUE INDEX idx_task_reminders_unique ON task_reminders(task_id, fire_at, kind);
CREATE INDEX idx_task_reminders_pending ON task_reminders(status, fire_at);

-- Inbox items are not all tasks. An idea or a note is kept as captured and can be
-- revisited later instead of being forced into the task list.
ALTER TABLE inbox_items ADD COLUMN kind TEXT NOT NULL DEFAULT 'task'
    CHECK(kind IN ('task','idea','note','goal'));
ALTER TABLE inbox_items ADD COLUMN revisit_at DATE;

CREATE INDEX idx_inbox_kind ON inbox_items(kind, created_at DESC);
CREATE INDEX idx_inbox_revisit ON inbox_items(revisit_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_inbox_revisit;
DROP INDEX IF EXISTS idx_inbox_kind;
ALTER TABLE inbox_items DROP COLUMN revisit_at;
ALTER TABLE inbox_items DROP COLUMN kind;

DROP TABLE IF EXISTS task_reminders;
DROP TABLE IF EXISTS task_blocks;

DROP INDEX IF EXISTS idx_tasks_repeat_parent;
ALTER TABLE tasks DROP COLUMN repeat_parent_id;
ALTER TABLE tasks DROP COLUMN repeat;
ALTER TABLE tasks DROP COLUMN expected_result;
ALTER TABLE tasks DROP COLUMN estimate_minutes;

-- +goose StatementEnd
