-- The turn inspector (P5-04): what a person took out of a chat's next
-- turn. See docs/architecture/inspector.md. internal/inspector owns this
-- table.

-- One row for each recalled page or source, or pinned message, a person
-- took out of a chat's next turn. kind is page, source or message, and ref
-- is the page's or source's id in the knowledge store, or the message's
-- id. label is what the inspector called it. turn_id is empty while the
-- skip waits; the chat's next turn to build a model call claims it, with
-- taken_at, every call of that turn leaves it out, and no later turn does.
-- A chat has at most one waiting skip of one item. The rows go with their
-- chat.
CREATE TABLE chat_skips (
    id       INTEGER PRIMARY KEY,
    chat_id  TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    kind     TEXT NOT NULL CHECK (kind IN ('page', 'source', 'message')),
    ref      TEXT NOT NULL CHECK (ref <> ''),
    label    TEXT NOT NULL DEFAULT '',
    asked_at TEXT NOT NULL,
    turn_id  TEXT NOT NULL DEFAULT '',
    taken_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE UNIQUE INDEX chat_skips_waiting ON chat_skips (chat_id, kind, ref) WHERE turn_id = '';
CREATE INDEX chat_skips_by_turn ON chat_skips (turn_id) WHERE turn_id <> '';
