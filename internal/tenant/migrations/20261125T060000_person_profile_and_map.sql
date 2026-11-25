-- The person's profile and the map of their knowledge store (P6-04). See
-- docs/architecture/knowledge-profile-and-map.md. internal/knowledge/personmap
-- owns these tables.

-- The profile: a short text about the person, in their own words, which
-- starts every call to a model. One row at most. No row means no profile.
CREATE TABLE person_profile (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    text       TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- The map as it was last built. One row at most. catalog_key says which
-- state of the catalog it was built from: the store's catalog marker, or a
-- hash of the catalog for a store with no marker. shape says which limits
-- and which layout it was built with. A map whose key or shape is not
-- today's is built again.
--
-- whole_text is for a model that may read sensitive content. open_text is
-- for one that may not, and holds nothing from a sensitive space but the
-- number of spaces held back, which is withheld_spaces.
CREATE TABLE person_map (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    catalog_key     TEXT NOT NULL,
    shape           TEXT NOT NULL,
    whole_text      TEXT NOT NULL,
    open_text       TEXT NOT NULL,
    spaces          INTEGER NOT NULL,
    pages           INTEGER NOT NULL,
    withheld_spaces INTEGER NOT NULL,
    built_at        TEXT NOT NULL
) STRICT;
