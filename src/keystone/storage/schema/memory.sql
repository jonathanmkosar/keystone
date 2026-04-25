-- memory.db: persistent observations, entities, rules, scheduled reports.

CREATE TABLE IF NOT EXISTS observations (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp         INTEGER NOT NULL,           -- unix epoch seconds (created_at; immutable)
    type              TEXT    NOT NULL,           -- fact | preference | observation | scheduled_report | rule
    entity            TEXT,                       -- optional FK to entities.name
    content           TEXT    NOT NULL,
    source            TEXT,                       -- e.g. 'user', 'cowork:morning_brief'
    embedding         BLOB,                       -- optional float32[1536] when embedded
    last_updated_at   INTEGER,                    -- last meaningful change via update_memory; NULL if never edited
    deleted_at        INTEGER,                    -- soft-delete timestamp; NULL = active
    superseded_by_id  INTEGER,                    -- forward pointer set when supersede_memory replaces this row
    supersedes_id     INTEGER                     -- backward pointer: this row was created as a replacement for that id
);

CREATE INDEX IF NOT EXISTS idx_observations_type      ON observations(type);
CREATE INDEX IF NOT EXISTS idx_observations_entity    ON observations(entity);
CREATE INDEX IF NOT EXISTS idx_observations_timestamp ON observations(timestamp DESC);
-- Note: indexes on deleted_at and superseded_by_id are created by
-- migrations._migrate_memory_v2 after the columns are guaranteed to exist
-- (either by this CREATE TABLE for fresh installs, or by ADD COLUMN for
-- pre-v2 installs).

CREATE TABLE IF NOT EXISTS entities (
    name        TEXT PRIMARY KEY,           -- canonical entity key
    kind        TEXT,                       -- person | account | team | project | ...
    aliases     JSON,                       -- ["Joe", "Joseph"]
    facts       JSON,                       -- arbitrary structured facts
    updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS rules (
    pattern     TEXT PRIMARY KEY,           -- e.g. "payee:STARBUCKS"
    value       JSON NOT NULL,              -- e.g. {"category": "Coffee"}
    updated_at  INTEGER NOT NULL
);

-- semantic_index lives in data.db OR memory.db depending on which content
-- it indexes. The memory variant indexes only observations.content.
CREATE VIRTUAL TABLE IF NOT EXISTS semantic_index USING vec0(
    embedding   float[1536],
    +source_type TEXT,
    +source_ref  TEXT
);
