-- Browser use (P10-03): the screenshots the agent's own browser took, one
-- row for each, shown under the tool step that took it. call_id is the
-- model's tool call, url and title the page shown, data the JPEG. The rows
-- go when their chat is deleted. See docs/architecture/browser-use.md.
-- internal/tools/browser owns this table.
CREATE TABLE browser_shots (
    id         TEXT PRIMARY KEY,
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    call_id    TEXT NOT NULL,
    url        TEXT NOT NULL,
    title      TEXT NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type = 'image/jpeg'),
    data       BLOB NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX browser_shots_by_chat ON browser_shots(chat_id);
