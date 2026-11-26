-- Uploads (P9-02): the files and images a person adds to a chat, and what
-- each model call of a turn was given of them. See
-- docs/architecture/uploads.md. internal/uploads owns these tables.

-- One row for each file added to a chat. hash is the file's SHA-256 in
-- lower-case hex: its name in the tenant's files folder, the ref a
-- message's image or file block carries, and the upload's id in the API.
-- The same file added to one chat twice is one row. kind is image (sent as
-- an image to a model that can see), text (its text is read and goes into
-- a call as an attachment) or document (sent whole to a model that takes
-- files; no text is read). text is the text read from a text file. The
-- file itself is tied to the chat in chat_files, so deleting the chat
-- removes it when no other chat holds it.
CREATE TABLE uploads (
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    hash       TEXT NOT NULL CHECK (length(hash) = 64),
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('image', 'text', 'document')),
    media_type TEXT NOT NULL,
    size       INTEGER NOT NULL CHECK (size > 0),
    text       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (chat_id, hash)
) STRICT, WITHOUT ROWID;

CREATE INDEX uploads_by_chat ON uploads(chat_id, created_at);

-- One row for each model call of a turn whose branch held an image or a
-- file: what the call was given of each, and what was withheld and why,
-- as an uploads.Call in JSON, for the turn inspector and the notice to the
-- person. step counts the turn's calls from 1. message_id is the person's
-- message the turn answers. The rows go with the turn's chat.
CREATE TABLE upload_calls (
    turn_id    TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    step       INTEGER NOT NULL,
    message_id INTEGER NOT NULL DEFAULT 0,
    record     TEXT NOT NULL,
    PRIMARY KEY (turn_id, step)
) STRICT, WITHOUT ROWID;

CREATE INDEX upload_calls_by_chat ON upload_calls(chat_id);
