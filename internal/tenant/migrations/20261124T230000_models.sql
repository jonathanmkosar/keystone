-- A tenant's model registry: one row for each model the tenant can use.
-- entry is the whole entry as JSON, checked before it is stored and again
-- each time it is read, so a new field needs no migration. The entry names
-- the secret that holds a key and never holds the key itself.
-- internal/models owns this table.
CREATE TABLE models (
    name       TEXT PRIMARY KEY,
    entry      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
