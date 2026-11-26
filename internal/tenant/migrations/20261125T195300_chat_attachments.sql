-- Attached chats and handoff (P5-06): a chat can attach other chats of the
-- same person, and each call of it carries their summaries; a handoff opens
-- a fresh chat that carries the chat it continues from. See
-- docs/architecture/attach-and-handoff.md. internal/attach owns this table.

-- One row for each chat attached to a chat. chat_id is the chat that
-- carries attached_id. handoff is 1 on the row that says chat_id was handed
-- off from attached_id, its parent: at most one such row for each chat, and
-- it is not taken off by hand. Both columns refer to this tenant's own
-- chats, so a chat of another tenant cannot be named, and the row goes when
-- either chat is deleted. seq keeps the order the chats were attached in.
CREATE TABLE chat_attachments (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id     TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    attached_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    handoff     INTEGER NOT NULL DEFAULT 0 CHECK (handoff IN (0, 1)),
    added_at    TEXT NOT NULL,
    UNIQUE (chat_id, attached_id),
    CHECK (chat_id <> attached_id)
) STRICT;

CREATE INDEX chat_attachments_by_attached ON chat_attachments (attached_id, handoff);

CREATE UNIQUE INDEX chat_attachments_one_parent ON chat_attachments (chat_id) WHERE handoff = 1;
