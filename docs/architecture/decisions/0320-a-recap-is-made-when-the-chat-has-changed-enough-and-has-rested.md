# 0320. A recap is made when the chat has changed enough and has rested, by the summary route, under the turn's request id

November 25, 2026 · decided in P5-05

## The question

The plan's chat record keeps "a summary, topics, decisions" for each chat, and the item says they are kept up to date by a job after turns, not after every turn, when the chat has changed enough. When exactly is a chat given a new one, which model writes it, and how is it recorded?

## The decision

**When.** After a turn ends with a whole reply, a chat is due a recap when it has none, or when `chats.recap_after_turns` (3) more turns have ended since its recap was made. A due chat is not recapped at once: a goroutine waits until the chat has rested for `chats.recap_after_idle` (2 minutes) since its last turn, and a turn that ends while it waits moves the wait on. When the wait ends, the chat must still be due and have no turn waiting or running. A person can also ask for one now, `POST /chats/{id}/recap`, which makes it the same way and answers with it.

**By what.** The model routing gives the `summary` task, the route compaction uses: `routing.summary_model`, then the chat's own model, then the fallback chain, behind the budget's guard and the monthly cap. The call is made with compaction's step, so its usage record is of the kind `summary`.

**Recorded under** the request id of the turn whose end started the wait, as compaction's summaries and the memory pass are; a recap asked for through the route is recorded under that call's.

**Kept in memory, not queued.** Which chats wait, and which failed, is held by the runner. A failure waits `chats.recap_retry_after` before a turn's end tries again.

## Why

- Counting turns is the plainest "changed enough" a person can set. The first recap after the first turn means even a one-question chat can be found by its summary.
- Waiting for the chat to rest makes a recap of where the chat ended, not of its middle, and saves the calls a lively exchange would otherwise make. It also keeps the recap's call out of the turn's own stream of calls, which several checks count.
- The summary route already routes a held chat to models that may read it, falls back, and is capped; a recap is a summary.
- The turn's request id lets a background task's budget ([jobs.md](../jobs.md)) and the turn inspector see the call that the turn caused, as they see compaction's.
- A recap is cheap to make again and a stale one does no harm, so a queue that survives a restart, as the memory pass has, is not worth a table: a chat left due is picked up at its next turn's end.

## What it costs

- A chat whose last turn ended just before a stop waits for its next turn, or the route, to be given its newest recap.
- Chats made before this item have no recap until a turn ends in them. A sweep belongs with P8-01's jobs.
- A recap's usage is of the kind `summary`, beside compaction's, so a report of spend by kind does not tell the two apart. The audit log's request id and the recap's own `request_id` do.
