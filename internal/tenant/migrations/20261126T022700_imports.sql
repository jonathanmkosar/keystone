-- Imports (P12-03): what was brought into this tenant from another app's
-- database, so that a second run brings nothing twice and a chat that came
-- from elsewhere says so. See docs/architecture/import.md. internal/imported
-- owns these tables.

-- One row for each run that wrote something. id is 16 random hex
-- characters. source names the app the data came from, new-keystone today,
-- and workspace the workspace of it that was read. file_sha256 is the
-- SHA-256 of the file that was read, which says which copy it was and holds
-- none of its words. request_id names the run's entries in the audit log.
-- chats, messages and schedules count what the run wrote. ended_at is empty
-- for a run that did not finish; what it wrote stays, and a later run goes
-- on from there.
CREATE TABLE imports (
    id          TEXT PRIMARY KEY CHECK (length(id) = 16),
    source      TEXT NOT NULL CHECK (source <> ''),
    workspace   TEXT NOT NULL,
    file_sha256 TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL,
    chats       INTEGER NOT NULL DEFAULT 0 CHECK (chats >= 0),
    messages    INTEGER NOT NULL DEFAULT 0 CHECK (messages >= 0),
    schedules   INTEGER NOT NULL DEFAULT 0 CHECK (schedules >= 0),
    started_at  TEXT NOT NULL,
    ended_at    TEXT NOT NULL DEFAULT ''
) STRICT;

-- One row for each thing of the source that was brought in: a turn (a
-- person's message and the reply to it) or a routine. source_id is its id
-- there. chat_id is the chat a turn went into, and schedule_id the schedule
-- a routine became. A row is written in the same transaction as what it
-- names, and is never removed: a chat or a schedule the person deletes
-- later does not come back with the next run. It holds ids and no words.
CREATE TABLE import_sources (
    source      TEXT NOT NULL CHECK (source <> ''),
    workspace   TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('turn', 'routine')),
    source_id   TEXT NOT NULL CHECK (source_id <> ''),
    import_id   TEXT NOT NULL,
    chat_id     TEXT NOT NULL DEFAULT '',
    schedule_id TEXT NOT NULL DEFAULT '',
    imported_at TEXT NOT NULL,
    PRIMARY KEY (source, workspace, kind, source_id)
) STRICT, WITHOUT ROWID;

-- The mark an imported chat wears. kind says what it was in the source: a
-- stretch of the continuing chat, a routine's run, or a build. turns is how
-- many turns it holds and unanswered how many of them have no reply.
-- first_at and last_at are the times of its first and last message there.
-- models is a JSON list of the models the source named for its replies,
-- and steps a JSON object counting the events the source recorded in it, by
-- type. note says, for a person, what the source recorded that the chat
-- record cannot hold. The row goes with its chat.
CREATE TABLE imported_chats (
    chat_id     TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    import_id   TEXT NOT NULL,
    source      TEXT NOT NULL CHECK (source <> ''),
    kind        TEXT NOT NULL CHECK (kind IN ('chat', 'routine', 'build')),
    turns       INTEGER NOT NULL CHECK (turns >= 0),
    unanswered  INTEGER NOT NULL DEFAULT 0 CHECK (unanswered >= 0),
    first_at    TEXT NOT NULL,
    last_at     TEXT NOT NULL,
    models      TEXT NOT NULL DEFAULT '[]',
    steps       TEXT NOT NULL DEFAULT '{}',
    note        TEXT NOT NULL DEFAULT '',
    imported_at TEXT NOT NULL
) STRICT;

CREATE INDEX imported_chats_by_import ON imported_chats(import_id);

-- What the source recorded about an imported message: the turn it belongs
-- to there, and for a reply the model the source named. No turn of
-- Keystone's wrote the message, so turn_messages has no row for it. The row
-- goes with its message.
CREATE TABLE imported_messages (
    message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    source_id  TEXT NOT NULL,
    model      TEXT NOT NULL DEFAULT ''
) STRICT;
