-- Scheduled tasks (P8-03): a prompt on a schedule, in the person's time
-- zone, whose every run is a background task working in a chat of its own,
-- so each run is a chat. A missed run happens once. See
-- docs/architecture/schedules.md. internal/schedules owns these tables.

-- One row for each schedule. kind is how it recurs: hourly at minute;
-- daily, weekdays, or weekly on days (mon,tue,…) at hour and minute; or
-- once, on on_date (YYYY-MM-DD) at hour and minute. model is the registry
-- name of the model its runs name, empty for a new chat's own. incognito
-- makes each run's chat incognito, so no memory pass reads it. enabled is
-- off while the person has paused it. zone is the time zone next_at was
-- worked out in, empty for the machine's: a change of the person's zone
-- moves next_at. next_at is the next due time, in UTC, and empty when there
-- is none: a one-time schedule that has run.
CREATE TABLE schedules (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    id         TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    prompt     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('hourly', 'daily', 'weekdays', 'weekly', 'once')),
    hour       INTEGER NOT NULL DEFAULT 0 CHECK (hour BETWEEN 0 AND 23),
    minute     INTEGER NOT NULL DEFAULT 0 CHECK (minute BETWEEN 0 AND 59),
    days       TEXT NOT NULL DEFAULT '',
    on_date    TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    incognito  INTEGER NOT NULL DEFAULT 0 CHECK (incognito IN (0, 1)),
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    zone       TEXT NOT NULL DEFAULT '',
    next_at    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- One row for each run. cause is schedule (a due time came) or now (the
-- person asked). due_at is the due time it was for, the latest of those
-- that passed when several did, or when the person asked. missed is how
-- many due times it stands for: one, or more when the server was down over
-- several. late says it started more than agents.schedule_late_after after
-- due_at, and late_reason why: stopped, Keystone was not running at the
-- due time; busy, the run before was still going or too many tasks were
-- open. state is starting until its background task is written, then
-- started; failed when it could not start, with error saying why. A run
-- that is starting and has an error is waiting to start, and is tried
-- again. request_id is the call or event that started it, and actor who
-- its task runs for. chat_id is its chat, made first, and job_id its task.
CREATE TABLE schedule_runs (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    id          TEXT NOT NULL UNIQUE,
    schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    cause       TEXT NOT NULL CHECK (cause IN ('schedule', 'now')),
    due_at      TEXT NOT NULL,
    missed      INTEGER NOT NULL DEFAULT 1 CHECK (missed >= 0),
    late        INTEGER NOT NULL DEFAULT 0 CHECK (late IN (0, 1)),
    late_reason TEXT NOT NULL DEFAULT '' CHECK (late_reason IN ('', 'stopped', 'busy')),
    state       TEXT NOT NULL CHECK (state IN ('starting', 'started', 'failed')),
    request_id  TEXT NOT NULL,
    actor       TEXT NOT NULL,
    chat_id     TEXT NOT NULL DEFAULT '',
    job_id      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
) STRICT;

CREATE INDEX schedule_runs_by_schedule ON schedule_runs(schedule_id, seq);
CREATE INDEX schedule_runs_starting ON schedule_runs(state) WHERE state = 'starting';

-- A due time is run once, whatever happens: a second run for it is refused
-- by the database itself.
CREATE UNIQUE INDEX schedule_runs_once_per_due ON schedule_runs(schedule_id, due_at) WHERE cause = 'schedule';
