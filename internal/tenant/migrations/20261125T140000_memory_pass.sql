-- The memory pass (P6-06): after a turn ends, a model reads the exchange
-- and the durable facts it holds are added to the person's knowledge store
-- as one source that points back to the chat. See
-- docs/architecture/memory-pass.md. internal/knowledge/memory owns these
-- tables.

-- One row for each turn whose pass was queued. status moves from queued to
-- running, and then to done, skipped or failed; a pass a stop left running
-- is marked interrupted and is not run again, because it may have added
-- its source already. reason says why a pass was skipped, failed or
-- interrupted. model is the model that read the exchange. space,
-- source_id, sensitive and facts are where a pass that landed put its
-- facts, the source the store made, whether that space is sensitive, and
-- the facts as a JSON list of text; facts that landed nowhere are not kept
-- here either. The rows go when the turn's chat is deleted.
CREATE TABLE memory_passes (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id    TEXT NOT NULL UNIQUE REFERENCES turns(id) ON DELETE CASCADE,
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'skipped', 'failed', 'interrupted')),
    reason     TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    space      TEXT NOT NULL DEFAULT '',
    source_id  TEXT NOT NULL DEFAULT '',
    sensitive  INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    facts      TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    ended_at   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX memory_passes_by_chat ON memory_passes (chat_id, status, seq);

-- One row for each chat whose memory flag was ever set. incognito is 1 for
-- a chat whose turns run no memory pass. A chat with no row is not
-- incognito. The row goes with its chat.
CREATE TABLE memory_chats (
    chat_id    TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    incognito  INTEGER NOT NULL DEFAULT 0 CHECK (incognito IN (0, 1)),
    changed_at TEXT NOT NULL
) STRICT, WITHOUT ROWID;
