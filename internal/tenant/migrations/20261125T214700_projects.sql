-- Projects (P9-01): instructions, knowledge files, and the chats in a
-- project. See docs/architecture/projects.md. internal/projects owns these
-- tables.

-- One row for each project. id is the project's id in the API. memory_space
-- is the knowledge store space its chats' facts go to, named when the
-- project is made and kept when it is renamed; empty sends them to the
-- person's ordinary memory space. archived_at is set while the project is
-- archived: it is kept, and the screens show it apart.
CREATE TABLE projects (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    memory_space TEXT NOT NULL DEFAULT '',
    archived_at  TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
) STRICT;

-- One row for each knowledge file of a project. hash is the file's SHA-256
-- in lower-case hex: its name in the tenant's files folder and its id in
-- the API. The same file added to one project twice is one row. text is
-- the text read from it. sensitive is 1 when the person marked the file
-- sensitive: it then reaches only calls whose every model may read
-- sensitive content. The rows go with the project; the file in the folder
-- goes when no project file and no chat names it.
CREATE TABLE project_files (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    hash       TEXT NOT NULL CHECK (length(hash) = 64),
    name       TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size       INTEGER NOT NULL CHECK (size > 0),
    text       TEXT NOT NULL DEFAULT '',
    sensitive  INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    added_at   TEXT NOT NULL,
    PRIMARY KEY (project_id, hash)
) STRICT, WITHOUT ROWID;

CREATE INDEX project_files_by_hash ON project_files(hash);

-- One row for each chat in a project: a chat is in one project or none.
-- Both columns refer to this tenant's own rows, so a chat or a project of
-- another tenant cannot be named, and the row goes when either is deleted.
-- Deleting a project so leaves its chats as ordinary chats.
CREATE TABLE project_chats (
    chat_id    TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    added_at   TEXT NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX project_chats_by_project ON project_chats(project_id, added_at);
