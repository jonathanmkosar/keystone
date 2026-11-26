-- Wiki upkeep jobs (P8-06). See docs/architecture/wiki-jobs.md.
-- internal/wikijobs owns both tables.

-- One row for each wiki job a person has had a schedule for: the schedule
-- (P8-03) the job owns, and a digest of the settings it was last made
-- from, so a change of the settings is carried to the schedule once.
CREATE TABLE wiki_jobs (
    job         TEXT PRIMARY KEY CHECK (job <> ''),
    schedule_id TEXT NOT NULL DEFAULT '',
    synced      TEXT NOT NULL DEFAULT '',
    changed_at  TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- One row for each run of a wiki job whose task has ended, written before
-- its answer is applied so a restart never applies it twice. run_id is the
-- schedule run's id. state is applying while Keystone writes what the
-- answer asked for, then applied, refused (the answer asked for something
-- the job may not do, so nothing was written), failed (a write went
-- wrong; what was written before it stays), nothing (the run did not
-- finish, or asked for nothing), or interrupted (the server stopped while
-- it was applying). written is what was written, as JSON. note_turn is
-- the turn of the note that tells the run's chat, chosen before it is
-- written.
CREATE TABLE wiki_runs (
    run_id    TEXT PRIMARY KEY CHECK (run_id <> ''),
    job       TEXT NOT NULL,
    chat_id   TEXT NOT NULL,
    task_id   TEXT NOT NULL DEFAULT '',
    state     TEXT NOT NULL CHECK (state IN ('applying', 'applied', 'refused', 'failed', 'nothing', 'interrupted')),
    summary   TEXT NOT NULL DEFAULT '',
    written   TEXT NOT NULL DEFAULT '[]',
    note_turn TEXT NOT NULL DEFAULT '',
    at        TEXT NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX wiki_runs_by_chat ON wiki_runs(chat_id);
