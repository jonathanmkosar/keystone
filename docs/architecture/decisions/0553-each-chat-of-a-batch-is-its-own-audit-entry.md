# 0553. Each chat of a batch delete is its own audit entry

November 26, 2026 · decided in P4-06

## The question

The audit log records every API call by its method and path, before it runs and when it ends, and never its body (decision [0009](0009-audit-log-append-only.md)): passwords and keys travel in bodies. `DELETE /chats/{id}` is therefore in the log with the chat's id. `POST /chats/delete` names its chats in the body, so its own two entries say only that some chats were deleted. How does the log say which?

## The decision

- **Each chat of the batch is written as an entry of its own**, by the handler, under the call's request id, with the kind `api`, the actor `person`, and the action its own delete would have had: `DELETE /api/v1/chats/<id>`.
- **A chat that is deleted has two entries**, `started` before the delete and `ok` after, as a call has. A delete that cannot be recorded first is not made, and the chat is skipped as `failed`.
- **A chat that is left has one**, `refused` with the status its own call would have answered (404, 409) and the reason in `received`; a failure is `error`.
- **An id is checked before it is written.** A body with an id that is not a short run of letters, digits, hyphens and underscores is refused whole, so nothing a caller types at length reaches the log.

## Why

- Deleting is the one thing here that cannot be taken back. A log that says "a batch was deleted" and not what was in it could not answer "when did that chat go, and by which call".
- The same action text as the single delete means one search of the log finds a chat's deletion however it was deleted, and the request id ties the entries of one batch together and to the call.
- Writing the ids into the call's own entry would mean recording part of a body, which the log's rule forbids for every route. An entry per chat keeps that rule whole.
- The entry before the delete follows the log's own principle: nothing happens that could not be recorded.

## What it costs

- A batch of 100 chats writes about 200 entries. They are short, and the log is never trimmed.
- The log holds `DELETE` actions for calls that were not `DELETE` requests. Their request id leads to the `POST /api/v1/chats/delete` that made them, in the same log.
- An id of another person's chat that someone puts in their batch is in the caller's own log, as it would be in the path of a single delete. It is in nobody else's.
