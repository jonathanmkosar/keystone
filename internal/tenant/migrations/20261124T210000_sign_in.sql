-- Sign-in for one tenant (P2-02). A tenant is one person, so there is no
-- table of users: a tenant has at most one password and any number of
-- passkeys. Times are UTC text, such as 2026-11-24T21:00:00Z.

-- The password, as a salted slow hash. failures counts the attempts since
-- the last good sign-in; last_failure is when the latest one was made.
CREATE TABLE signin_password (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    algorithm    TEXT    NOT NULL,
    iterations   INTEGER NOT NULL,
    salt         BLOB    NOT NULL,
    hash         BLOB    NOT NULL,
    set_at       TEXT    NOT NULL,
    failures     INTEGER NOT NULL DEFAULT 0,
    last_failure TEXT    NOT NULL DEFAULT ''
) STRICT;

-- One row for each signed-in browser or device. token_hash is the SHA-256
-- of the session token. The token itself is never stored.
CREATE TABLE signin_sessions (
    id         TEXT PRIMARY KEY,
    token_hash BLOB NOT NULL UNIQUE,
    method     TEXT NOT NULL CHECK (method IN ('password', 'passkey')),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
) STRICT;

-- One row for each passkey. public_key is the key as the browser gives it
-- (SubjectPublicKeyInfo). It is public, and useless without the device.
CREATE TABLE signin_passkeys (
    id            TEXT PRIMARY KEY,
    credential_id BLOB    NOT NULL UNIQUE,
    public_key    BLOB    NOT NULL,
    sign_count    INTEGER NOT NULL,
    name          TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    last_used_at  TEXT    NOT NULL DEFAULT ''
) STRICT;

-- A challenge handed to a browser, waiting to come back signed. Each is
-- used once and then deleted.
CREATE TABLE signin_challenges (
    challenge  BLOB PRIMARY KEY,
    purpose    TEXT NOT NULL CHECK (purpose IN ('add', 'sign-in')),
    expires_at TEXT NOT NULL
) STRICT;
