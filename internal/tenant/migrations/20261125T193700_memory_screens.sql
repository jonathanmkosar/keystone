-- The memory screens (P6-07): view, correct and forget what the memory
-- pass added, and incognito chats that keep no record. See
-- docs/architecture/memory-screens.md. internal/knowledge owns
-- knowledge_withdrawn, and internal/knowledge/memory memory_chats.

-- One row for each source a person asked Keystone to forget, in a store
-- that cannot remove a source, such as Waymarks, which keeps every source
-- unchanged. Every store knowledge.Open hands back leaves these ids out of
-- its searches, its reads and the citations of its pages. adapter is the
-- store's adapter, because an id means something only to the store that
-- gave it. The row keeps no word of the source.
CREATE TABLE knowledge_withdrawn (
    adapter      TEXT NOT NULL CHECK (adapter <> ''),
    source_id    TEXT NOT NULL CHECK (source_id <> ''),
    withdrawn_at TEXT NOT NULL,
    PRIMARY KEY (adapter, source_id)
) STRICT, WITHOUT ROWID;

-- left_at is when the person left an incognito chat, or empty while they
-- have not. A chat that was left is deleted as soon as no background task
-- works in it or was started from it; one that was not is deleted once it
-- has been idle for knowledge.incognito_keep.
ALTER TABLE memory_chats ADD COLUMN left_at TEXT NOT NULL DEFAULT '';
