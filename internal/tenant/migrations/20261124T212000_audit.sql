-- The audit log: one row for each thing recorded about a call. Rows are
-- added and read, and nothing else. The triggers below refuse a change, a
-- delete and a replace, whichever code asks. internal/audit owns this table.
CREATE TABLE audit_log (
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
    input_tokens       INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens      INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    thinking_tokens    INTEGER NOT NULL DEFAULT 0 CHECK (thinking_tokens >= 0),
    tokens_estimated   INTEGER NOT NULL DEFAULT 0 CHECK (tokens_estimated IN (0, 1)),
    cost_micros        INTEGER NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
    duration_ms        INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0)
) STRICT;

CREATE INDEX audit_log_request ON audit_log(request_id);

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'the audit log is append-only: an entry cannot be changed');
END;

CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'the audit log is append-only: an entry cannot be deleted');
END;

-- INSERT OR REPLACE removes the old row without firing the delete trigger,
-- so an insert that names an id already in the log is refused here. An
-- insert that names no id reaches this trigger with NEW.id as -1, which the
-- table's own check keeps out of the log, so it never matches a row.
CREATE TRIGGER audit_log_no_replace BEFORE INSERT ON audit_log
WHEN EXISTS (SELECT 1 FROM audit_log WHERE id = NEW.id)
BEGIN
    SELECT RAISE(ABORT, 'the audit log is append-only: an entry cannot be replaced');
END;
