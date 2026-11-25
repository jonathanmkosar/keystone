-- The built-in knowledge store (P6-02): spaces, pages, sources, page
-- vectors and the search index. See docs/architecture/knowledge-builtin.md.
-- The tables are empty for a tenant who chose another store, or none.

-- One row for each space, with its label. internal/knowledge/builtin reads
-- the label in the same query as the content, every time. A space with no
-- row here, or with any label but 'normal', reads as sensitive. That is why
-- the tables below do not reference this one: a lost row must not hide or
-- remove content, it must only make it sensitive.
CREATE TABLE knowledge_spaces (
    name  TEXT PRIMARY KEY,
    label TEXT NOT NULL CHECK (label IN ('normal', 'sensitive'))
) STRICT, WITHOUT ROWID;

-- One row for each page. The body is Markdown. A page is named by its space
-- and path, and keeps its id when it is written again. revision goes up by
-- one each time the page changes. Ids are never used twice.
CREATE TABLE knowledge_pages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    space      TEXT NOT NULL,
    path       TEXT NOT NULL,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL,
    revision   INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    UNIQUE (space, path)
) STRICT;

-- One row for each source. A source is kept as it was brought in and is
-- never changed.
CREATE TABLE knowledge_sources (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    space    TEXT NOT NULL,
    name     TEXT NOT NULL,
    origin   TEXT NOT NULL DEFAULT '',
    dated    TEXT NOT NULL DEFAULT '',
    text     TEXT NOT NULL,
    added_at TEXT NOT NULL
) STRICT;

CREATE INDEX knowledge_sources_by_space ON knowledge_sources(space, id);

-- The sources a page cites, in the order the page cites them.
CREATE TABLE knowledge_page_sources (
    page_id   INTEGER NOT NULL REFERENCES knowledge_pages(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    source_id INTEGER NOT NULL REFERENCES knowledge_sources(id),
    PRIMARY KEY (page_id, position)
) STRICT, WITHOUT ROWID;

-- One vector for each page: the model that made it, the page revision it was
-- made from, and the numbers as little-endian 32-bit floats. size is how
-- many numbers. A vector whose revision is not the page's is stale.
CREATE TABLE knowledge_page_vectors (
    page_id  INTEGER PRIMARY KEY REFERENCES knowledge_pages(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    model    TEXT NOT NULL,
    size     INTEGER NOT NULL,
    vector   BLOB NOT NULL
) STRICT;

-- Small values the store keeps for itself. 'catalog_changes' counts the
-- changes to the catalog, and is the catalog marker.
CREATE TABLE knowledge_meta (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- Full-text search. A row's rowid is knowledge_pages.id in the first table
-- and knowledge_sources.id in the second. internal/knowledge/builtin writes
-- and removes these rows in the same transaction as the row they index.
-- Words are stemmed, so "tomato" finds "tomatoes".
CREATE VIRTUAL TABLE knowledge_page_search USING fts5(title, body, tokenize = 'porter unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE knowledge_source_search USING fts5(name, text, tokenize = 'porter unicode61 remove_diacritics 2');
