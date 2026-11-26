-- Unattended spend and the pause switch (P8-05). See
-- docs/architecture/unattended-spend.md. internal/unattended owns the two
-- new tables; the columns added to jobs and schedules are read by
-- internal/jobs and internal/schedules.

-- One row for each unattended run: a background task, a scheduled run, or,
-- once P8-02 lands, a sub-agent. request_id is the run's own: every turn
-- of the run is sent under it, so its spend is the sum of the usage
-- records that carry it, and the budget gate finds the run from a turn's
-- request id. kind and ref_id say what the run is (ref_id is the task's
-- id, or the sub-agent's), title names it for a person, and budget_micros
-- is what it may spend on models that are not local.
CREATE TABLE unattended_runs (
    request_id    TEXT PRIMARY KEY CHECK (request_id <> ''),
    kind          TEXT NOT NULL CHECK (kind IN ('task', 'schedule', 'subagent')),
    ref_id        TEXT NOT NULL,
    title         TEXT NOT NULL,
    budget_micros INTEGER NOT NULL CHECK (budget_micros >= 0),
    created_at    TEXT NOT NULL
) STRICT;

-- The tasks that were started before this migration are runs too. A task
-- that works in the chat it was started from is a scheduled run (P8-03).
INSERT INTO unattended_runs(request_id, kind, ref_id, title, budget_micros, created_at)
    SELECT request_id, CASE WHEN chat_id = work_chat_id THEN 'schedule' ELSE 'task' END, id, title, budget_micros, created_at FROM jobs;

-- One row each time a call of an unattended run was refused at the
-- monthly cap or at the run's budget, and the route went on to a local
-- model (fallback) or the call was ended because unattended work may not
-- fall back (stop). model is the model that was not sent the call, and
-- fallback_model the one the route went on to. message is the refusal, for
-- a person. note_turn is the turn of the note that tells the run's chat,
-- chosen before the note is written so a restart never writes it twice,
-- and noted says it is written.
CREATE TABLE unattended_notices (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id     TEXT NOT NULL,
    turn_id        TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('fallback', 'stop')),
    model          TEXT NOT NULL,
    fallback_model TEXT NOT NULL DEFAULT '',
    fallback_class TEXT NOT NULL DEFAULT '',
    message        TEXT NOT NULL,
    note_turn      TEXT NOT NULL DEFAULT '',
    noted          INTEGER NOT NULL DEFAULT 0 CHECK (noted IN (0, 1)),
    at             TEXT NOT NULL
) STRICT;

CREATE INDEX unattended_notices_by_run ON unattended_notices(request_id, seq);

-- A task the switch holds (agents.paused). Its status stays queued or
-- running, so it still counts as open; the API shows it as paused.
-- paused_turn is the round the switch stopped, which a new round follows
-- when the switch goes off, and pause_note_turn the turn of the note that
-- says so in the task's chat, cleared once the note is written.
ALTER TABLE jobs ADD COLUMN paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1));
ALTER TABLE jobs ADD COLUMN paused_turn TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN pause_note_turn TEXT NOT NULL DEFAULT '';

-- A schedule's own budget for each of its runs, in millionths of a dollar.
-- -1 means the person's agents.schedule_budget_usd.
ALTER TABLE schedules ADD COLUMN budget_micros INTEGER NOT NULL DEFAULT -1 CHECK (budget_micros >= -1);
