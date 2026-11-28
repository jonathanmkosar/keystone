-- Auto in the turn (P14-04). A chat is on Auto, where the router chooses
-- the model and the effort for each message, or on the model picked. A
-- chat made before this migration keeps its model: it is not on Auto. A
-- new chat takes router.auto_default when it is made.
ALTER TABLE chats ADD COLUMN auto INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1));

-- How a turn on Auto was routed, kept on the turn so its tool steps and a
-- turn resumed after an approval use the same model and effort, and the
-- reply's footnote can say why. ladder_step counts from 1, smallest first,
-- of ladder_steps. verdict_id names the turn's row in router_verdicts.
-- stronger_of is the turn this one writes again one step up ("Try a
-- stronger model"), or empty.
ALTER TABLE turns ADD COLUMN auto INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1));
ALTER TABLE turns ADD COLUMN ladder_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN ladder_steps INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN route_label TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN route_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN route_notice TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN route_switched INTEGER NOT NULL DEFAULT 0 CHECK (route_switched IN (0, 1));
ALTER TABLE turns ADD COLUMN verdict_id TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN stronger_of TEXT NOT NULL DEFAULT '';

-- One row each time a message on Auto was routed: the router's record
-- (router.Record) with its ids and time. shown is the facts the decision
-- model was shown, as JSON, without the message's text, which is in the
-- chat. step_answered, probability and effort_answered are the verdict's,
-- empty with none; step and effort are what was chosen. rules_only_why is
-- one of the router's Only names. stronger_asked is set on the row of a
-- turn the person asked to write again one step up. The row goes with its
-- turn, and so with its chat.
CREATE TABLE router_verdicts (
    id                TEXT PRIMARY KEY,
    turn_id           TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    request_id        TEXT NOT NULL,
    decision_model    TEXT NOT NULL DEFAULT '',
    shown             TEXT NOT NULL,
    step_answered     TEXT NOT NULL DEFAULT '',
    probability       REAL NOT NULL DEFAULT 0,
    effort_answered   TEXT NOT NULL DEFAULT '',
    step              TEXT NOT NULL,
    effort            TEXT NOT NULL,
    moved_up          INTEGER NOT NULL DEFAULT 0 CHECK (moved_up IN (0, 1)),
    timed_out         INTEGER NOT NULL DEFAULT 0 CHECK (timed_out IN (0, 1)),
    rules_only        INTEGER NOT NULL DEFAULT 0 CHECK (rules_only IN (0, 1)),
    rules_only_why    TEXT NOT NULL DEFAULT '',
    latency_ms        INTEGER NOT NULL DEFAULT 0,
    local_unavailable INTEGER NOT NULL DEFAULT 0 CHECK (local_unavailable IN (0, 1)),
    stronger_asked    INTEGER NOT NULL DEFAULT 0 CHECK (stronger_asked IN (0, 1)),
    reason            TEXT NOT NULL DEFAULT '',
    notice            TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
) STRICT;

CREATE INDEX router_verdicts_by_turn ON router_verdicts(turn_id, created_at);
CREATE INDEX router_verdicts_by_time ON router_verdicts(created_at);
