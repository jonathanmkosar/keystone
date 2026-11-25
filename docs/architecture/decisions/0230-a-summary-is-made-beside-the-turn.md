# 0230. A summary is made beside the turn, and the call goes with what is stored

November 25, 2026 · decided in P5-02

## The question

When a chat's earlier turns no longer fit, the oldest are summarised by a model call. That call takes seconds with a real model. Is it made before the chat call that needs it, so that call carries the new summary, or beside it?

## The decision

**Beside it.** After each chat call is built, its record says whether the chat has outgrown the room for its earlier turns. If it has, a summary job starts in a goroutine of its own, with a store of its own, and the chat call is sent at once with what was stored before: the old summary, or none, and the turns left out with the note as before. The next call carries the new summary.

**One job at a time for a chat**, claimed in the runner's memory. A tool step that builds again while a job runs starts none.

**A failure costs one line in the log and nothing else.** A route with no model, a refused or failed call, an empty answer, a branch that changed under the job, or `chats.compact_timeout` running out: nothing is stored, the chat goes on with the note, and the chat is not tried again for `chats.compact_retry_after`.

**The summary call is a model call like any other.** It goes through the route routing gives the `summary` task, with the budget's guard and the monthly cap on every model, under the turn's request id. `Runner.call` makes it with its own step, `stepSummary`, which `Runner.used` records with the kind `summary`. So it has its two audit entries and its usage record, and counts against the cap.

**The runner waits for the job when it closes**, as it waits for a turn, and a job cut short by closing writes nothing more.

## Why

- The plan says summarising never blocks a turn. A summary that held the reply would add a model call's time to the turn, in exactly the long chats where replies are already slow.
- The summary is made ahead of need (decision [0232](0232-when-a-chat-is-summarised.md)), so the call that goes without it is usually one whose turns still fit, and the note is rare.
- Every outcome of a failure is today's behaviour, which P5-01 already proved.

## What it costs

- The call that starts the first summary of a chat goes without it. If that call already had to leave turns out, it leaves them out with the note.
- Two model calls of one tenant can run at once, so the cap's known gap, two calls each reading the month before the other is recorded, is a little more likely.
- A timed-out summary call is recorded as a stopped call, as a timed-out title is.
- The memory of who is summarising and who failed is the runner's: a restart forgets a failure and may try again at once.
