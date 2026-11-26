# 0430. A usage record is placed in a chat and a kind of work by its request id

November 25, 2026 · decided in P11-02

## The question

The usage report breaks spend down by chat and by kind of work: chat, background task, scheduled task, memory pass, summaries. A usage record holds the kind of task it was routed for and the request id of the API call that set it off, and no chat and no task. Should the record gain a chat column and a job column, or should the report work out where a record belongs from what else the tenant's database holds?

## The decision

The record is not changed. The report places each request id once, in this order:

1. A background task whose own request id it is: the kind of work is `background_task`, or `scheduled_task` when a schedule run started the task, and the chat is the task's work chat.
2. A turn sent under it: the turn's chat.
3. The API call in the audit log, when its path is a chat's or under one: that chat.

A chat the tenant no longer has, or none, puts the calls on one line, "Not in a chat you still have". Outside a task, a record's kind of work is its own kind. An index on `turns(request_id, seq)` makes the second step a lookup.

## Why

- Every model call already carries the request id that ties it to its turn or its task: decision [0293](0293-a-task-has-its-own-request-id-and-budget.md) gave a task its own for its budget, and the turn's summary, recap, title and memory calls are made under the turn's (decision [0320](0320-a-recap-is-made-when-the-chat-has-changed-enough-and-has-rested.md)). The place is there to be found.
- Changing how a record is written would touch the turn runner, the memory pass, the jobs runner and every later writer, while two other lanes were adding writers of their own (embeddings) and readers of the same records (unattended spend). Reading changes nothing they rely on.
- A column written at the time would be no truer for a deleted chat: the chat is gone either way, and its calls belong on a line of their own.
- The task comes first because its note in the chat it was started from is a turn under the task's request id, and its calls were made in its own chat.

## What it costs

- A placement can change after the fact: when a chat is deleted, its calls move to "Not in a chat you still have"; when the chat a task was started from is deleted, its task row goes with it, and the task's calls become the kind they were routed as. The totals never change, only which line holds them.
- Compaction and recaps both write the kind `summary`, and nothing tells them apart, so the report shows them as one kind, "Summaries and recaps".
- A report reads the jobs, schedule runs, turns, chats and audit tables once for each request id in its period. With the index that is a few lookups each.
- A later writer of usage records that uses a request id of its own, with no turn or task behind it, lands on the line of calls in no chat until it is added to this order.
