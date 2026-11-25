-- What each turn's recall found (P6-05): which pages and sources the
-- search of the knowledge store brought back, which were kept for the call
-- and which were dropped, and why. See
-- docs/architecture/knowledge-recall.md. internal/knowledge/recall owns
-- this table.

-- One row for each turn of a person who has a knowledge store, and for
-- each turn a lookup sent sensitive content in. record is a recall.Record
-- as JSON, for the turn inspector, and empty when a lookup alone marked
-- the turn. sensitive is 1 when the turn sent a model content from a
-- sensitive space: every later call of that chat is then held to models
-- that may read it. The rows go when the turn's chat is deleted.
CREATE TABLE turn_recall (
    turn_id   TEXT PRIMARY KEY REFERENCES turns(id) ON DELETE CASCADE,
    chat_id   TEXT NOT NULL,
    sensitive INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    record    TEXT NOT NULL DEFAULT ''
) STRICT, WITHOUT ROWID;

CREATE INDEX turn_recall_held ON turn_recall (chat_id, sensitive);
