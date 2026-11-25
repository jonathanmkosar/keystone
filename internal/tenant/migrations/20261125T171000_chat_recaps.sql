-- Chat recaps (P5-05): each chat's short summary, its topics and the
-- decisions made in it, which the past-chat search finds chats by. See
-- docs/architecture/chat-summaries.md. internal/recap owns both tables.

-- One row for each chat that has a recap. A newer recap takes the row's
-- place. topics and decisions are JSON lists of text. through_id is the
-- last message of the branch it was made from, and turns how many of the
-- chat's turns had ended with a whole reply then. sensitive is 1 when the
-- chat had been sent content from a sensitive space when this recap or one
-- before it was made, and is never set back to 0: the recap then reaches a
-- call only when every model of the call may read such content.
-- request_id names the call it was made under in the audit log. The row
-- goes with its chat.
CREATE TABLE chat_recaps (
    chat_id    TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    summary    TEXT NOT NULL CHECK (summary <> ''),
    topics     TEXT NOT NULL DEFAULT '[]',
    decisions  TEXT NOT NULL DEFAULT '[]',
    through_id INTEGER NOT NULL DEFAULT 0,
    turns      INTEGER NOT NULL DEFAULT 0 CHECK (turns >= 0),
    model      TEXT NOT NULL,
    sensitive  INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    request_id TEXT NOT NULL DEFAULT '',
    made_at    TEXT NOT NULL
) STRICT;

-- Full-text search of the recaps. A row's rowid is its recap's rowid. The
-- triggers keep it in step with chat_recaps, a recap that goes with its
-- chat included.
CREATE VIRTUAL TABLE chat_recap_search USING fts5(summary, topics, decisions, tokenize = 'unicode61 remove_diacritics 2');

CREATE TRIGGER chat_recaps_added AFTER INSERT ON chat_recaps BEGIN
    INSERT INTO chat_recap_search(rowid, summary, topics, decisions) VALUES (new.rowid, new.summary, new.topics, new.decisions);
END;

CREATE TRIGGER chat_recaps_changed AFTER UPDATE ON chat_recaps BEGIN
    DELETE FROM chat_recap_search WHERE rowid = old.rowid;
    INSERT INTO chat_recap_search(rowid, summary, topics, decisions) VALUES (new.rowid, new.summary, new.topics, new.decisions);
END;

CREATE TRIGGER chat_recaps_removed AFTER DELETE ON chat_recaps BEGIN
    DELETE FROM chat_recap_search WHERE rowid = old.rowid;
END;
