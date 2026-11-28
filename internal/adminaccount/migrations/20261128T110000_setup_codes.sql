-- Setup codes (P15-02, decision 0703). An admin adds a person, or gives one
-- a new code, and the person enters it at /setup to set their own password.
-- code_hash is the SHA-256 of the code under a fixed label; the code itself
-- is never kept. A code is good once (used_at), until expires_at, for the
-- one person tenant_id names; a newer code for that person ends it
-- (ended_at). issued_by is the admin account that made it. Times are UTC
-- text, such as 2026-11-28T11:00:00Z; '' is not yet.
CREATE TABLE setup_codes (
    code_hash  BLOB PRIMARY KEY,
    tenant_id  TEXT NOT NULL CHECK (length(tenant_id) = 16),
    issued_by  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT NOT NULL DEFAULT '',
    ended_at   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX setup_codes_by_tenant ON setup_codes(tenant_id, expires_at);