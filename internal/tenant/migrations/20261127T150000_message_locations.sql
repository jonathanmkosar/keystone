-- The approximate location a person shared with one of their messages
-- (P10-06): only on their tap, rounded before it left the phone and again
-- here, and kept with that message alone. line is the sentence the model is
-- given in the person's message, as it was written when the message was
-- placed, so every later call sends the same bytes. The row goes with its
-- message, and so with its chat.
CREATE TABLE message_locations (
    message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    latitude   REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude  REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    decimals   INTEGER NOT NULL CHECK (decimals BETWEEN 1 AND 4),
    line       TEXT NOT NULL
) STRICT;
