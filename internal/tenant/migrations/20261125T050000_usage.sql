-- Usage (P3-08): one row for each model call, with its tokens and its cost.
-- See docs/architecture/usage.md. internal/usage owns this table.
--
-- month is the first seven characters of at, the calendar month in UTC,
-- kept as a column so a month's rows are found by an index. kind is the
-- kind of task the call was made for, and class is the model's class when
-- the call was made: the monthly cap counts the rows whose class is not
-- local. cost_micros is millionths of a US dollar.
--
-- Rows are added and read, and nothing else, so what was spent cannot be
-- taken back to get under the cap. Deleting a chat leaves its calls here.
CREATE TABLE usage_records (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id > 0),
    at                 TEXT    NOT NULL,
    month              TEXT    NOT NULL CHECK (length(month) = 7 AND month = substr(at, 1, 7)),
    request_id         TEXT    NOT NULL DEFAULT '',
    kind               TEXT    NOT NULL CHECK (kind <> ''),
    model              TEXT    NOT NULL CHECK (model <> ''),
    class              TEXT    NOT NULL CHECK (class IN ('local', 'hosted', 'trusted')),
    outcome            TEXT    NOT NULL CHECK (outcome IN ('ok', 'stopped', 'refused', 'error')),
    input_tokens       INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens      INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    thinking_tokens    INTEGER NOT NULL DEFAULT 0 CHECK (thinking_tokens >= 0),
    tokens_estimated   INTEGER NOT NULL DEFAULT 0 CHECK (tokens_estimated IN (0, 1)),
    cost_micros        INTEGER NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
    duration_ms        INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0)
) STRICT;

CREATE INDEX usage_records_month ON usage_records(month, model);
CREATE INDEX usage_records_request ON usage_records(request_id);

CREATE TRIGGER usage_records_no_update BEFORE UPDATE ON usage_records
BEGIN
    SELECT RAISE(ABORT, 'usage records are append-only: a record cannot be changed');
END;

CREATE TRIGGER usage_records_no_delete BEFORE DELETE ON usage_records
BEGIN
    SELECT RAISE(ABORT, 'usage records are append-only: a record cannot be deleted');
END;

-- INSERT OR REPLACE removes the old row without firing the delete trigger,
-- so an insert that names an id already there is refused, as in the audit
-- log.
CREATE TRIGGER usage_records_no_replace BEFORE INSERT ON usage_records
WHEN EXISTS (SELECT 1 FROM usage_records WHERE id = NEW.id)
BEGIN
    SELECT RAISE(ABORT, 'usage records are append-only: a record cannot be replaced');
END;
