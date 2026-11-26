# 0512. An imported chat has messages and no turns

November 26, 2026 · decided in P12-03

## The question

A turn of New Keystone's (a job) is very like a turn here: a person's message, a reply, a status, a model. Keystone keeps what a turn recorded about a message (its model, usage and cost) in `turn_messages`, which hangs on a row in `turns`. Should each imported job be written as a turn, so its model shows through the paths that are there?

## The decision

No. **An imported chat is messages only**, as the test tenant's loaded chats are. What the source recorded is kept in tables of the import's own (`internal/imported`):

- `imported_chats`, the mark the chat wears;
- `imported_messages`, the source's turn for each message and the model it named for a reply.

`GET /chats/{id}` gives an imported reply its `model` from there, with no `turn_id`, `usage` or `cost_micros`. The one exception is a chat the admin asked to be held (decision [0513](0513-imported-chats-are-not-held-unless-the-admin-says-so.md)): the mark that holds a chat hangs on a turn, so such a chat has one turn that calls no model.

## Why

- `turns` is read by parts that mean "something Keystone ran": Operations lists the newest turns by the order they were written, so a hundred imported turns would be its whole list of recent work; a turn's request id names its model calls in the audit log and its usage records, and an imported turn has neither; the inspector opens on a turn and would open on nothing.
- A chat's next turn uses the model its last turn used. New Keystone's model names are not names in the person's registry. The code skips a name it does not know today, and nothing should rest on that.
- Rule 4 says every model call is recorded. A row in `turns` with a model and no call in the audit log would read as a call that was not recorded.
- A table of the import's own can be read, changed and dropped without touching the turn package, and goes with the chat by `ON DELETE CASCADE` like every other.

## What it costs

- Two added lines in the chat routes and one field on `Chat`, to show the mark and the model.
- A failed turn's status and error are not kept as such: the chat shows the person's message with no reply, and the mark counts how many there are.
- New Keystone's token counts for its calls are not brought. They are in its `events` table, measured its way, and Keystone's usage report is built only from its own usage records.
