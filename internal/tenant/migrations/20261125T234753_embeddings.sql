-- Embeddings (P6-08): where each of a person's refreshes of their page
-- vectors stands, one row for each knowledge store adapter, replaced by the
-- next refresh. The vectors themselves are kept by the store: the built-in
-- store in knowledge_page_vectors, Waymarks in its own database. See
-- docs/architecture/embeddings.md.
CREATE TABLE embedding_state (
    adapter      TEXT PRIMARY KEY,
    -- The registry name of the embedding model, its class then, and the
    -- name the vectors carry. Empty when there was no model.
    model        TEXT NOT NULL DEFAULT '',
    class        TEXT NOT NULL DEFAULT '',
    vector_model TEXT NOT NULL DEFAULT '',
    at           TEXT NOT NULL,
    listed       INTEGER NOT NULL DEFAULT 0,
    embedded     INTEGER NOT NULL DEFAULT 0,
    -- Pages in a sensitive space left without a vector, because the model
    -- may not read sensitive content.
    held         INTEGER NOT NULL DEFAULT 0,
    stale        INTEGER NOT NULL DEFAULT 0,
    failed       INTEGER NOT NULL DEFAULT 0,
    waiting      INTEGER NOT NULL DEFAULT 0,
    stopped      TEXT NOT NULL DEFAULT '',
    -- What the person is told: why nothing was done, or what was left.
    note         TEXT NOT NULL DEFAULT ''
);
