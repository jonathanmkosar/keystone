-- Connectors (P7-04): each person's MCP servers, the tools each one last
-- listed, and the person's switch for each connector in each chat. See
-- docs/architecture/connectors.md. internal/connectors owns these tables.

-- One row for each connector. name is lower-case letters, digits and
-- underscores, and starts the name of each of its tools. transport is stdio
-- (a command run on this machine, with its arguments as a JSON array and
-- plain environment variables as a JSON object) or http (the streamable
-- HTTP transport at url; local says the person marked it as their own
-- server on this machine or their home network). secret names the vault
-- entry that holds its token, and token_env, for stdio, the variable the
-- token is passed in; the token itself is never here. outside says its
-- results are outside content. added_by is person (through the API) or
-- command line. listed_at is when its tools were last listed, and problem
-- why the last try to reach it failed, in words a person reads.
CREATE TABLE connectors (
    name       TEXT PRIMARY KEY CHECK (name <> ''),
    label      TEXT NOT NULL DEFAULT '',
    transport  TEXT NOT NULL CHECK (transport IN ('stdio', 'http')),
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

-- The tools a connector listed when it was last reached, in the order it
-- listed them. tool is the server's own name; schema is its input schema
-- as JSON. read_only is the server's readOnlyHint. A turn offers these by
-- name, and the approvals screen lists them, without reaching the server.
CREATE TABLE connector_tools (
    connector   TEXT NOT NULL REFERENCES connectors(name) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    tool        TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    schema      TEXT NOT NULL,
    read_only   INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    PRIMARY KEY (connector, tool)
) STRICT, WITHOUT ROWID;

-- The person's switch for one connector in one chat. A chat with no row for
-- a connector has it on, when the connector is enabled. A connector that is
-- not enabled is off in every chat, whatever its row says. The rows go with
-- the chat and with the connector.
CREATE TABLE chat_connectors (
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    connector  TEXT NOT NULL REFERENCES connectors(name) ON DELETE CASCADE,
    switched_on INTEGER NOT NULL CHECK (switched_on IN (0, 1)),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (chat_id, connector)
) STRICT, WITHOUT ROWID;
