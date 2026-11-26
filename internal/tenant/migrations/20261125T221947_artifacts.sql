-- Artifacts (P9-03): the documents a model makes in a chat, shown in a
-- panel beside it, with every version kept. See
-- docs/architecture/artifacts.md. internal/artifacts owns these tables.

-- One row for each artifact. id is 16 random hex characters, the id the
-- model, the API and the publish link name it by. An artifact belongs to
-- the chat it was made in and goes with it. kind is what it is drawn as,
-- and language names a code artifact's language. title and latest are the
-- newest version's, kept here so a list needs no join.
CREATE TABLE artifacts (
    id         TEXT PRIMARY KEY CHECK (length(id) = 16),
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('code', 'html', 'react', 'svg', 'mermaid', 'markdown')),
    language   TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL,
    latest     INTEGER NOT NULL CHECK (latest > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX artifacts_by_chat ON artifacts(chat_id, created_at);
CREATE INDEX artifacts_by_update ON artifacts(updated_at);

-- One row for each version of an artifact, numbered from 1. The content is
-- a file in the tenant's artifacts folder, <chat>/<artifact>/v<version>;
-- size and sha256 are its length in bytes and its SHA-256 in lower-case
-- hex. origin says who wrote it: the model through a tool, or the person by
-- restoring an older version (restored_from names it). sensitive says the
-- chat was held for content from a sensitive space when it was written.
-- request_id is the API call it was written for, which the audit log names.
CREATE TABLE artifact_versions (
    artifact_id   TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL CHECK (version > 0),
    title         TEXT NOT NULL,
    size          INTEGER NOT NULL CHECK (size >= 0),
    sha256        TEXT NOT NULL CHECK (length(sha256) = 64),
    origin        TEXT NOT NULL CHECK (origin IN ('model', 'restore')),
    restored_from INTEGER NOT NULL DEFAULT 0,
    sensitive     INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    request_id    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    PRIMARY KEY (artifact_id, version)
) STRICT, WITHOUT ROWID;
