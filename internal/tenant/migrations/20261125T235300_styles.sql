-- Styles (P9-05): a person's own saved tone and format presets, and the
-- style each chat uses. See docs/architecture/styles.md. internal/styles
-- owns these tables.

-- One row for each style the person made. id is the style's id in the API:
-- sixteen hexadecimal characters, which no built-in style's id may be. The
-- built-in styles are not rows: they come from the setting styles.builtin.
CREATE TABLE styles (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
) STRICT;

-- The style a chat uses, when it has one of its own: a built-in style's id
-- or a row of styles. A chat with no row uses the person's default style,
-- and its first turn keeps that default here. chat_id refers to this
-- tenant's own chats, so another tenant's chat cannot be given a style, and
-- the row goes with the chat. Deleting a style removes the rows that name
-- it.
CREATE TABLE chat_styles (
    chat_id   TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    style_id  TEXT NOT NULL,
    chosen_at TEXT NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX chat_styles_by_style ON chat_styles(style_id);
