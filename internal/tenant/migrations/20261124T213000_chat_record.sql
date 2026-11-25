-- The chat record (P4-02): chats, their messages, the files a chat holds,
-- and the search index. See docs/architecture/chat.md.

-- One row for each chat. id is what the rest of Keystone names a chat by.
-- seq is the row's number, which the title search index is keyed by.
-- current_root is the first message of the current branch.
-- active_at is when the last message was written.
CREATE TABLE chats (
    seq          INTEGER PRIMARY KEY,
    id           TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL DEFAULT '',
    starred      INTEGER NOT NULL DEFAULT 0 CHECK (starred IN (0, 1)),
    current_root INTEGER REFERENCES messages(id),
    created_at   TEXT NOT NULL,
    active_at    TEXT NOT NULL
) STRICT;

CREATE INDEX chats_by_activity ON chats(active_at DESC, seq DESC);

-- Messages are a tree. parent_id is the message this one answers or follows,
-- and is empty for a first message. An edit or a retry is another message
-- with the same parent. current_child is the child the current branch goes
-- through. content is a seam.Message as JSON. Ids are never used twice.
CREATE TABLE messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id       TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    parent_id     INTEGER REFERENCES messages(id),
    current_child INTEGER REFERENCES messages(id),
    role          TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content       TEXT NOT NULL,
    created_at    TEXT NOT NULL
) STRICT;

CREATE INDEX messages_by_parent ON messages(chat_id, parent_id, id);

-- The files a chat holds, by their name in the tenant's files folder.
CREATE TABLE chat_files (
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    name    TEXT NOT NULL,
    PRIMARY KEY (chat_id, name)
) STRICT, WITHOUT ROWID;

CREATE INDEX chat_files_by_name ON chat_files(name);

-- Full-text search. A row's rowid is chats.seq in the first table and
-- messages.id in the second. internal/chat writes and removes these rows in
-- the same transaction as the row they index.
CREATE VIRTUAL TABLE chat_title_search USING fts5(title, tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE message_search USING fts5(body, tokenize = 'unicode61 remove_diacritics 2');
