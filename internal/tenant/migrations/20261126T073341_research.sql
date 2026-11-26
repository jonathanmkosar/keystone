-- Research mode (P8-04): a long report from many sources, built by
-- sub-agents and saved as an artifact. See
-- docs/architecture/research-mode.md. internal/research owns this table.

-- One row for each research task. chat_id is the chat it was started from,
-- where its report and its note go; work_chat_id is the chat of its own
-- that its plan and its report are written in, each as a turn sent under
-- request_id, the task's own, which is also the key of its run in
-- unattended_runs. started_request is the API call that started it, actor
-- who it runs for, and model the model its turns and its parts' name (empty
-- for the chat's own).
--
-- status is queued, running, and then done, stopped or failed. stage says
-- where a task that has not ended is: plan, parts, write. paused says the
-- switch that pauses every agent holds it; its status stays as it was, so
-- it still counts as open, and the API shows it as paused.
--
-- budget_micros is the whole task's budget, its parts included, and
-- spent_micros what it and they have spent, written as it goes. max_parts,
-- sources_per_part and report_words are the settings as it started.
--
-- plan is the parts the plan step gave, a JSON list of {title, task}.
-- plan_turn_id and write_turn_id are the turns of its own chat for the
-- plan and the report, and call_id names its parts among the sub-agents of
-- its own chat. sources is a JSON list of the sources its parts read,
-- numbered across all of them, written before the report step, and then the
-- ones the report cites, renumbered, once it is written; sources_read
-- counts every source the parts read, cited or not. dropped counts the
-- citations taken out because they named no source a part read.
--
-- report is the report as it is saved, summary what the note says of it,
-- at_budget whether it stopped at its budget, and notice what else the
-- person is told: a part that did not finish, a model that stood in.
-- artifact_id and artifact_chat_id are the artifact the report was saved
-- as, and the chat it is in: the chat the task was started from, or its
-- own when its report may not go there (fixed rule 3). error is a
-- ResearchError as JSON on one that failed. note_turn_id is chosen when the
-- task ends, and note_message_id is the note once it is written in chat_id.
CREATE TABLE research (
    seq               INTEGER PRIMARY KEY AUTOINCREMENT,
    id                TEXT NOT NULL UNIQUE,
    chat_id           TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    work_chat_id      TEXT NOT NULL,
    title             TEXT NOT NULL,
    question          TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'stopped', 'failed')),
    stage             TEXT NOT NULL DEFAULT '' CHECK (stage IN ('', 'plan', 'parts', 'write')),
    paused            INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    stop_requested    INTEGER NOT NULL DEFAULT 0 CHECK (stop_requested IN (0, 1)),
    request_id        TEXT NOT NULL UNIQUE,
    started_request   TEXT NOT NULL DEFAULT '',
    actor             TEXT NOT NULL,
    model             TEXT NOT NULL DEFAULT '',
    budget_micros     INTEGER NOT NULL CHECK (budget_micros >= 0),
    spent_micros      INTEGER NOT NULL DEFAULT 0,
    max_parts         INTEGER NOT NULL CHECK (max_parts >= 1),
    sources_per_part  INTEGER NOT NULL CHECK (sources_per_part >= 1),
    report_words      INTEGER NOT NULL CHECK (report_words >= 1),
    plan              TEXT NOT NULL DEFAULT '',
    plan_turn_id      TEXT NOT NULL DEFAULT '',
    call_id           TEXT NOT NULL,
    write_turn_id     TEXT NOT NULL DEFAULT '',
    sources           TEXT NOT NULL DEFAULT '',
    sources_read      INTEGER NOT NULL DEFAULT 0,
    dropped           INTEGER NOT NULL DEFAULT 0,
    report            TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    at_budget         INTEGER NOT NULL DEFAULT 0 CHECK (at_budget IN (0, 1)),
    notice            TEXT NOT NULL DEFAULT '',
    artifact_id       TEXT NOT NULL DEFAULT '',
    artifact_chat_id  TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    progress          TEXT NOT NULL DEFAULT '',
    note_turn_id      TEXT NOT NULL DEFAULT '',
    note_message_id   INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    started_at        TEXT NOT NULL DEFAULT '',
    ended_at          TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX research_by_chat ON research(chat_id, seq);
CREATE INDEX research_by_work_chat ON research(work_chat_id);
CREATE INDEX research_open ON research(status);
