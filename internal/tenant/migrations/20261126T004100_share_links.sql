-- Share links (P10-04): a read-only link to one chat, which someone who has
-- not signed in may open. See docs/architecture/share.md. internal/share
-- owns this table.

-- One row for each link. id is 16 random hex characters: it names the link
-- in lists and in the audit log, and opens nothing. token_hash is the
-- SHA-256 of the link's token; the token itself is never kept. view is
-- live (the chat as it is when the link is opened) or snapshot (the branch
-- up to upto, the last message there was when the link was made; empty for
-- live). expires_at is empty for a link that never expires, and revoked_at
-- for one that was not turned off; a revoked link keeps its row, so it
-- answers that it was turned off. views and viewed_at count its showings.
-- A link goes with its chat.
CREATE TABLE share_links (
    id         TEXT PRIMARY KEY CHECK (length(id) = 16),
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    view       TEXT NOT NULL CHECK (view IN ('live', 'snapshot')),
    upto       INTEGER,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL DEFAULT '',
    revoked_at TEXT NOT NULL DEFAULT '',
    views      INTEGER NOT NULL DEFAULT 0 CHECK (views >= 0),
    viewed_at  TEXT NOT NULL DEFAULT '',
    CHECK ((view = 'snapshot') = (upto IS NOT NULL))
) STRICT;

CREATE INDEX share_links_by_chat ON share_links(chat_id, created_at);
CREATE INDEX share_links_open ON share_links(revoked_at, expires_at);
