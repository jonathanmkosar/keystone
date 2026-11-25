-- The approval policy (P7-02): each person's policy for each tool, the
-- calls that asked for a person's answer and what became of them, and the
-- turns that read content from outside Keystone. See
-- docs/architecture/approvals.md. internal/turn owns these tables.

-- A person's own policy for a tool. A tool with no row has its default:
-- always for a tool that only reads, ask for one that changes something.
CREATE TABLE tool_policies (
    tool       TEXT PRIMARY KEY,
    policy     TEXT NOT NULL CHECK (policy IN ('always', 'ask', 'never')),
    updated_at TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- One row for each call of a turn that asked for a person's answer, and
-- for each call of a tool that changes something and ran under the
-- person's standing policy (status standing). step and message_id are the
-- model call and the stored reply whose calls they are, so a turn that
-- waits can go on from there after a restart. input is the call's
-- arguments as JSON. reason is policy or outside, and why the sentence
-- the person reads. decided_by and decided_request are the actor and the
-- request id of the API call that answered. used_at is set just before an
-- approved call runs: an approval is used once, and a call marked used is
-- never run again, also after a restart.
CREATE TABLE tool_approvals (
    id              TEXT PRIMARY KEY,
    turn_id         TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    chat_id         TEXT NOT NULL,
    step            INTEGER NOT NULL,
    message_id      INTEGER NOT NULL,
    call_id         TEXT NOT NULL,
    tool            TEXT NOT NULL,
    input           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    reason          TEXT NOT NULL,
    why             TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'declined', 'expired', 'stopped', 'standing')),
    asked_at        TEXT NOT NULL,
    decided_at      TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_request TEXT NOT NULL DEFAULT '',
    used_at         TEXT NOT NULL DEFAULT '',
    UNIQUE (turn_id, message_id, call_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX tool_approvals_by_turn ON tool_approvals(turn_id, step);

-- A turn that read content from outside Keystone: a tool whose result is
-- not Keystone's own, or a source of the knowledge store read or searched
-- with a lookup. Every tool that changes something asks in that turn and
-- in every later turn of the chat, since the content stays in the chat's
-- history. A source recall gave a call is read from turn_recall. The rows
-- go when the turn's chat is deleted.
CREATE TABLE tool_outside (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    tool    TEXT NOT NULL,
    why     TEXT NOT NULL,
    at      TEXT NOT NULL
) STRICT;

CREATE INDEX tool_outside_by_chat ON tool_outside(chat_id);
