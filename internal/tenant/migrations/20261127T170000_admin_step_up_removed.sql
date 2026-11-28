-- The admin step-up is gone (P15-01, decision 0653): an admin is a separate
-- account with sessions of its own in data/admin/admin.db, so a person's
-- session has no admin_until, and a passkey prompt has no 'step-up'
-- purpose. The two migrations that added them (20261127T160000 and
-- 20261127T160100) ran on installs already, so they stay, and this one
-- takes back what they added.
ALTER TABLE signin_sessions DROP COLUMN admin_until;

-- SQLite cannot change a CHECK, so the prompts table is made again, as
-- 20261127T160100 made it, with the two purposes it had before. A step-up
-- prompt in flight is dropped: nothing can answer it now.
CREATE TABLE signin_challenges_new (
    challenge  BLOB PRIMARY KEY,
    purpose    TEXT NOT NULL CHECK (purpose IN ('add', 'sign-in')),
    expires_at TEXT NOT NULL
) STRICT;
INSERT INTO signin_challenges_new(challenge, purpose, expires_at)
    SELECT challenge, purpose, expires_at FROM signin_challenges WHERE purpose IN ('add', 'sign-in');
DROP TABLE signin_challenges;
ALTER TABLE signin_challenges_new RENAME TO signin_challenges;
