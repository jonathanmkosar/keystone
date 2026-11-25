-- What each model call of a turn was built from (P5-01): the context
-- builder's record, for the turn inspector (P5-04). See
-- docs/architecture/context.md. internal/turn owns this table.

-- One row for each model call of a turn. step counts the turn's calls from
-- 1, as the turn's start and usage events do. record is a
-- contextbuild.Record as JSON: every part of the call with its tokens, and
-- every turn of the chat's branch with whether it was sent.
CREATE TABLE turn_contexts (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    step    INTEGER NOT NULL,
    record  TEXT NOT NULL,
    PRIMARY KEY (turn_id, step)
) STRICT, WITHOUT ROWID;
