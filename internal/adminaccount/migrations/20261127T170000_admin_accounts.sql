-- The admin accounts (P15-01, decision 0650). An admin account is kept
-- apart from people, in data/admin/admin.db: it has no tenant and no
-- tenant database, so it has nothing of a person's to show. Times are UTC
-- text, such as 2026-11-27T17:00:00Z.

-- One row for each admin account. name_key is the name in lower case, so
-- two accounts cannot share a name. Accounts are made and removed from the
-- command line alone (go run ./tools/admin).
CREATE TABLE accounts (
    id           TEXT PRIMARY KEY CHECK (length(id) = 16),
    name         TEXT NOT NULL CHECK (name <> ''),
    name_key     TEXT NOT NULL UNIQUE,
    added_at     TEXT NOT NULL,
    last_sign_in TEXT NOT NULL DEFAULT ''
) STRICT;

-- An account's password, as a salted slow hash, under the same settings as
-- a person's. failures counts the tries since the last good sign-in, and
-- last_failure is when the latest was made.
CREATE TABLE passwords (
    account_id   TEXT    PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    algorithm    TEXT    NOT NULL,
    iterations   INTEGER NOT NULL,
    salt         BLOB    NOT NULL,
    hash         BLOB    NOT NULL,
    set_at       TEXT    NOT NULL,
    failures     INTEGER NOT NULL DEFAULT 0,
    last_failure TEXT    NOT NULL DEFAULT ''
) STRICT;

-- One row for each signed-in browser. token_hash is the SHA-256 of the
-- session token, ksa_ and 32 random bytes as hex; the token is never kept.
-- last_used_at is when the session was last used, for admin.idle_for.
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    token_hash   BLOB NOT NULL UNIQUE,
    method       TEXT NOT NULL CHECK (method IN ('password', 'passkey')),
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    last_used_at TEXT NOT NULL
) STRICT;

CREATE INDEX sessions_account ON sessions(account_id);

-- One row for each passkey an account has added. public_key is the key as
-- the browser gives it (SubjectPublicKeyInfo).
CREATE TABLE passkeys (
    id            TEXT    PRIMARY KEY,
    account_id    TEXT    NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    credential_id BLOB    NOT NULL UNIQUE,
    public_key    BLOB    NOT NULL,
    sign_count    INTEGER NOT NULL,
    name          TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    last_used_at  TEXT    NOT NULL DEFAULT ''
) STRICT;

-- A passkey prompt waiting for its answer. A prompt to add a passkey names
-- the account it was given to; a sign-in prompt names none.
CREATE TABLE challenges (
    challenge  BLOB PRIMARY KEY,
    purpose    TEXT NOT NULL CHECK (purpose IN ('add', 'sign-in')),
    account_id TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL
) STRICT;

-- The admin audit log: every admin call, and every admin sign-in, in the
-- shape of a person's audit log. Rows are added and read, and nothing else:
-- the triggers refuse a change, a delete and a replace. An entry keeps the
-- account's id after the account is removed.
CREATE TABLE admin_audit_log (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id > 0),
    at                 TEXT    NOT NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('api', 'model', 'tool')),
    request_id         TEXT    NOT NULL CHECK (request_id <> ''),
    actor              TEXT    NOT NULL CHECK (actor <> ''),
    action             TEXT    NOT NULL CHECK (action <> ''),
    outcome            TEXT    NOT NULL CHECK (outcome IN ('started', 'ok', 'refused', 'error')),
    status             INTEGER NOT NULL DEFAULT 0,
    sent               TEXT    NOT NULL DEFAULT '',
    received           TEXT    NOT NULL DEFAULT '',
    duration_ms        INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0)
) STRICT;

CREATE INDEX admin_audit_log_request ON admin_audit_log(request_id);

CREATE TRIGGER admin_audit_log_no_update BEFORE UPDATE ON admin_audit_log
BEGIN
    SELECT RAISE(ABORT, 'the admin audit log is append-only: an entry cannot be changed');
END;

CREATE TRIGGER admin_audit_log_no_delete BEFORE DELETE ON admin_audit_log
BEGIN
    SELECT RAISE(ABORT, 'the admin audit log is append-only: an entry cannot be deleted');
END;

CREATE TRIGGER admin_audit_log_no_replace BEFORE INSERT ON admin_audit_log
WHEN EXISTS (SELECT 1 FROM admin_audit_log WHERE id = NEW.id)
BEGIN
    SELECT RAISE(ABORT, 'the admin audit log is append-only: an entry cannot be replaced');
END;
