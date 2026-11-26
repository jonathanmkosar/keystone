-- Sub-agents (P8-02): work a model hands to several helpers at once from a
-- turn, each in a chat of its own, whose results come back to that turn as
-- its tool's result. See docs/architecture/sub-agents.md.
-- internal/subagents owns this table.

-- One row for each sub-agent. chat_id is the chat it was started from, and
-- turn_id and call_id the turn there and the run_subagents call that
-- started it; position is its place among that call's sub-agents, from 1.
-- work_chat_id is the chat its turn runs in, made when it starts, and
-- work_turn_id that turn, once it is sent. request_id is its own: its turn
-- is sent under it, so its calls read together in the audit log, its spend
-- is the sum of the usage records that carry it, and it is the key of its
-- row in unattended_runs. actor is who it runs for, model the model its
-- turn names (empty for the parent chat's own), budget_micros what it may
-- spend on models that are not local. status is queued until it has a
-- lane, running, and then done, stopped, failed or paused (ended by the
-- switch that pauses every agent). steps, spent_micros, result, error (a
-- SubagentError as JSON) and notice are written when it ends; while it
-- runs they are read from its turn. outside and sensitive say its chat read
-- outside content or was held for sensitive content, written when it ends,
-- and passed that its result went back to the turn that started it.
-- stop_why says who stopped it, once someone has: the person, from its
-- lane; the parent turn, which was stopped; the timeout, when the call ran
-- past agents.subagent_timeout; or the switch that pauses every agent.
CREATE TABLE subagents (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    id             TEXT NOT NULL UNIQUE,
    chat_id        TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    turn_id        TEXT NOT NULL,
    call_id        TEXT NOT NULL,
    position       INTEGER NOT NULL CHECK (position >= 1),
    work_chat_id   TEXT NOT NULL,
    work_turn_id   TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL,
    task           TEXT NOT NULL,
    context        TEXT NOT NULL DEFAULT '',
    request_id     TEXT NOT NULL UNIQUE,
    actor          TEXT NOT NULL,
    model          TEXT NOT NULL DEFAULT '',
    budget_micros  INTEGER NOT NULL CHECK (budget_micros >= 0),
    max_steps      INTEGER NOT NULL CHECK (max_steps >= 1),
    status         TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'stopped', 'failed', 'paused')),
    steps          INTEGER NOT NULL DEFAULT 0,
    spent_micros   INTEGER NOT NULL DEFAULT 0,
    result         TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    notice         TEXT NOT NULL DEFAULT '',
    outside        INTEGER NOT NULL DEFAULT 0 CHECK (outside IN (0, 1)),
    sensitive      INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    passed         INTEGER NOT NULL DEFAULT 0 CHECK (passed IN (0, 1)),
    stop_why       TEXT NOT NULL DEFAULT '' CHECK (stop_why IN ('', 'person', 'parent', 'timeout', 'paused')),
    created_at     TEXT NOT NULL,
    started_at     TEXT NOT NULL DEFAULT '',
    ended_at       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX subagents_by_call ON subagents(chat_id, call_id, position);
CREATE INDEX subagents_by_turn ON subagents(turn_id);
CREATE INDEX subagents_by_work_chat ON subagents(work_chat_id);
CREATE INDEX subagents_open ON subagents(status);
