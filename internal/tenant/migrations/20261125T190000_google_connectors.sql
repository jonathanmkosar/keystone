-- Gmail and Google Calendar (P12-02): built-in connectors with a third
-- transport, google, and the person's Google sign-in. See
-- docs/architecture/google.md. internal/connectors owns these tables.
--
-- SQLite cannot change a CHECK in place, so the three connector tables are
-- made again with google among the transports, filled from the old ones,
-- and given back their names. The children are made against the new parent
-- and the old children are dropped before the old parent, so no foreign key
-- action fires; renaming the new parent rewrites its children's references.

CREATE TABLE connectors_next (
    name       TEXT PRIMARY KEY CHECK (name <> ''),
    label      TEXT NOT NULL DEFAULT '',
    transport  TEXT NOT NULL CHECK (transport IN ('stdio', 'http', 'google')),
    command    TEXT NOT NULL DEFAULT '',
    args       TEXT NOT NULL DEFAULT '[]',
    env        TEXT NOT NULL DEFAULT '{}',
    url        TEXT NOT NULL DEFAULT '',
    local      INTEGER NOT NULL DEFAULT 0 CHECK (local IN (0, 1)),
    secret     TEXT NOT NULL DEFAULT '',
    token_env  TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    outside    INTEGER NOT NULL DEFAULT 1 CHECK (outside IN (0, 1)),
    added_by   TEXT NOT NULL DEFAULT 'person',
    listed_at  TEXT NOT NULL DEFAULT '',
    problem    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT, WITHOUT ROWID;

INSERT INTO connectors_next(name, label, transport, command, args, env, url, local, secret, token_env, enabled, outside, added_by, listed_at, problem, created_at, updated_at)
    SELECT name, label, transport, command, args, env, url, local, secret, token_env, enabled, outside, added_by, listed_at, problem, created_at, updated_at FROM connectors;

CREATE TABLE connector_tools_next (
    connector   TEXT NOT NULL REFERENCES connectors_next(name) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    tool        TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    schema      TEXT NOT NULL,
    read_only   INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    PRIMARY KEY (connector, tool)
) STRICT, WITHOUT ROWID;

INSERT INTO connector_tools_next(connector, position, tool, title, description, schema, read_only)
    SELECT connector, position, tool, title, description, schema, read_only FROM connector_tools;

CREATE TABLE chat_connectors_next (
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    connector  TEXT NOT NULL REFERENCES connectors_next(name) ON DELETE CASCADE,
    switched_on INTEGER NOT NULL CHECK (switched_on IN (0, 1)),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (chat_id, connector)
) STRICT, WITHOUT ROWID;

INSERT INTO chat_connectors_next(chat_id, connector, switched_on, updated_at)
    SELECT chat_id, connector, switched_on, updated_at FROM chat_connectors;

DROP TABLE chat_connectors;
DROP TABLE connector_tools;
DROP TABLE connectors;

ALTER TABLE connectors_next RENAME TO connectors;
ALTER TABLE connector_tools_next RENAME TO connector_tools;
ALTER TABLE chat_connectors_next RENAME TO chat_connectors;

-- The person's Google sign-in: one row at most. email is the account that
-- signed in, from Google's identity token; scopes the scopes Google
-- granted, space-separated. The refresh token is in the person's vault
-- (connector.google), never here.
CREATE TABLE google_accounts (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    email        TEXT NOT NULL DEFAULT '',
    scopes       TEXT NOT NULL DEFAULT '',
    connected_at TEXT NOT NULL
) STRICT;

-- A sign-in that was started and not yet finished: its state, the PKCE
-- verifier, the redirect address it was started with and the services it
-- asks for. A row is used once and lives agents.google_sign_in_window.
CREATE TABLE google_pending (
    state      TEXT PRIMARY KEY,
    verifier   TEXT NOT NULL,
    redirect   TEXT NOT NULL,
    services   TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT, WITHOUT ROWID;
