-- Compaction (P5-02): the summaries that stand in a call for the oldest
-- turns of a long chat, and the messages a person pinned, which are never
-- left out. See docs/architecture/compaction.md. internal/compact owns
-- chat_summaries, and internal/chat owns chat_pins.

-- One row for each summary a chat was given. A summary stands for the
-- messages of one branch from its first, first_id, down to through_id:
-- messages are a tree, so the path to a message is that branch and no
-- other. extends is the summary it was made from, and 0 for a chat's
-- first, so a summary is extended and never made again from the start.
-- sensitive is 1 when the chat had been sent content from a sensitive
-- space when the summary was made: it then goes only to calls whose every
-- model may read such content. tokens is Keystone's estimate of the text.
-- turn_id names the turn whose call it was made beside. The rows go with
-- their chat.
CREATE TABLE chat_summaries (
    id         INTEGER PRIMARY KEY,
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    first_id   INTEGER NOT NULL,
    through_id INTEGER NOT NULL,
    turns      INTEGER NOT NULL CHECK (turns > 0),
    extends    INTEGER NOT NULL DEFAULT 0,
    text       TEXT NOT NULL CHECK (text <> ''),
    model      TEXT NOT NULL,
    sensitive  INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    tokens     INTEGER NOT NULL DEFAULT 0,
    turn_id    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX chat_summaries_by_chat ON chat_summaries (chat_id, through_id);

-- One row for each message a person pinned. A pinned message is in every
-- call of its chat's branch: in its turn, or on its own when its turn is
-- left out or summarised. The rows go with their chat.
CREATE TABLE chat_pins (
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    pinned_at  TEXT NOT NULL,
    PRIMARY KEY (chat_id, message_id)
) STRICT, WITHOUT ROWID;
