-- Turns (P4-03): a person's message and the reply to it, as a row that
-- outlives the request that sent it. See docs/architecture/turns.md.
-- internal/turn owns these tables.

-- One row for each turn. seq is the order turns arrived in, which is the
-- order a chat's queue is worked in. input is the body the turn was sent
-- with, kept so a turn that waits can be run later, also after a restart.
-- request_id and actor are those of the API call that sent it, and name the
-- turn's model calls in the audit log. error is a TurnError as JSON, and
-- usage is the seam's Usage summed over the turn's model calls.
-- last_event is the seq of the turn's newest row in turn_events.
CREATE TABLE turns (
    seq              INTEGER PRIMARY KEY AUTOINCREMENT,
    id               TEXT NOT NULL UNIQUE,
    chat_id          TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    status           TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'stopped', 'failed')),
    request_id       TEXT NOT NULL,
    actor            TEXT NOT NULL,
    input            TEXT NOT NULL,
    model            TEXT NOT NULL DEFAULT '',
    user_message_id  INTEGER NOT NULL DEFAULT 0,
    reply_message_id INTEGER NOT NULL DEFAULT 0,
    stop_reason      TEXT NOT NULL DEFAULT '',
    error            TEXT NOT NULL DEFAULT '',
    usage            TEXT NOT NULL DEFAULT '{"input_tokens":0,"output_tokens":0}',
    cost_micros      INTEGER NOT NULL DEFAULT 0,
    stop_requested   INTEGER NOT NULL DEFAULT 0 CHECK (stop_requested IN (0, 1)),
    last_event       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    started_at       TEXT NOT NULL DEFAULT '',
    ended_at         TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX turns_by_chat ON turns(chat_id, seq);
CREATE INDEX turns_open ON turns(status, chat_id, seq) WHERE status IN ('queued', 'running');

-- Every event a turn streamed, in order. seq counts from 1 within a turn
-- and is the event's id on the wire, so a client that lost its connection
-- asks for the rows after the last one it saw. data is the event's JSON.
CREATE TABLE turn_events (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    name    TEXT NOT NULL,
    data    TEXT NOT NULL,
    PRIMARY KEY (turn_id, seq)
) STRICT, WITHOUT ROWID;

-- What a turn recorded about a message it wrote: the turn, and for an
-- assistant message the model, why it stopped, the usage and the cost.
CREATE TABLE turn_messages (
    message_id  INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    turn_id     TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    model       TEXT NOT NULL DEFAULT '',
    stop_reason TEXT NOT NULL DEFAULT '',
    usage       TEXT NOT NULL DEFAULT '',
    cost_micros INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX turn_messages_by_turn ON turn_messages(turn_id, message_id);
