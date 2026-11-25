-- Background tasks (P8-01): a job that works through turns in a chat of
-- its own, outlives the request and the tab that started it, goes on
-- after a restart, and writes its result in the chat it was started from.
-- See docs/architecture/jobs.md. internal/jobs owns these tables.

-- One row for each task. chat_id is the chat it was started from, where
-- its result is written; work_chat_id is the chat its turns run in, made
-- when the task starts. request_id is the task's own, made for it: every
-- turn of the task is sent under it, so its model calls and tool calls
-- read together in the audit log, its spend is the sum of the usage
-- records that carry it, and its rounds are the turns of the work chat
-- that carry it. started_request is the API call that started it, and
-- actor who it runs for. model is the model its turns name, empty for the
-- work chat's own. budget_micros is what it may spend on models that are
-- not local. rounds, steps, turn_id, waiting and progress say where it is,
-- for a reader: they are written from its turns, which are the truth.
-- error is a TaskError as JSON. note_turn_id is the turn that writes its
-- result in chat_id, chosen before it is written so a restart never writes
-- it twice, and note_message_id is the message it wrote. last_event is the
-- seq of the task's newest row in job_events.
CREATE TABLE jobs (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    id              TEXT NOT NULL UNIQUE,
    chat_id         TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    work_chat_id    TEXT NOT NULL,
    title           TEXT NOT NULL,
    task            TEXT NOT NULL,
    started_by      TEXT NOT NULL CHECK (started_by IN ('person', 'tool')),
    status          TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'stopped', 'failed')),
    request_id      TEXT NOT NULL UNIQUE,
    started_request TEXT NOT NULL,
    actor           TEXT NOT NULL,
    model           TEXT NOT NULL DEFAULT '',
    budget_micros   INTEGER NOT NULL CHECK (budget_micros >= 0),
    max_rounds      INTEGER NOT NULL CHECK (max_rounds >= 1),
    rounds          INTEGER NOT NULL DEFAULT 0,
    steps           INTEGER NOT NULL DEFAULT 0,
    turn_id         TEXT NOT NULL DEFAULT '',
    waiting         INTEGER NOT NULL DEFAULT 0 CHECK (waiting IN (0, 1)),
    progress        TEXT NOT NULL DEFAULT '',
    spent_micros    INTEGER NOT NULL DEFAULT 0,
    result          TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    note_turn_id    TEXT NOT NULL DEFAULT '',
    note_message_id INTEGER NOT NULL DEFAULT 0,
    stop_requested  INTEGER NOT NULL DEFAULT 0 CHECK (stop_requested IN (0, 1)),
    last_event      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    started_at      TEXT NOT NULL DEFAULT '',
    ended_at        TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX jobs_by_chat ON jobs(chat_id, seq);
CREATE INDEX jobs_by_work_chat ON jobs(work_chat_id, seq);
CREATE INDEX jobs_unfinished ON jobs(status, note_message_id);

-- Every event a task streamed, in order, as turn_events holds a turn's.
-- seq counts from 1 within a task and is the event's id on the wire.
CREATE TABLE job_events (
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    seq    INTEGER NOT NULL,
    name   TEXT NOT NULL,
    data   TEXT NOT NULL,
    PRIMARY KEY (job_id, seq)
) STRICT, WITHOUT ROWID;
