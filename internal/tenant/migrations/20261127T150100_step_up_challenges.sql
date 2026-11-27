-- A passkey prompt for the admin step-up has a purpose of its own,
-- 'step-up', so a sign-in answer cannot step up and a step-up answer cannot
-- sign in (P15-01, decision 0643). SQLite cannot change a CHECK, so the
-- table is made again. Its rows are prompts that live for minutes; the ones
-- in flight are carried across.
CREATE TABLE signin_challenges_new (
    challenge  BLOB PRIMARY KEY,
    purpose    TEXT NOT NULL CHECK (purpose IN ('add', 'sign-in', 'step-up')),
    expires_at TEXT NOT NULL
) STRICT;
INSERT INTO signin_challenges_new(challenge, purpose, expires_at)
    SELECT challenge, purpose, expires_at FROM signin_challenges;
DROP TABLE signin_challenges;
ALTER TABLE signin_challenges_new RENAME TO signin_challenges;
