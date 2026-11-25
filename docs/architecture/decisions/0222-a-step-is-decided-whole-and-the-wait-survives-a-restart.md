# 0222. A step is decided whole before any call runs, and the turn waits in place, with the wait kept in the database

November 25, 2026 · decided in P7-02

## The question

A call that asks has to wait for a person, maybe for hours, and the item asks that the wait survive a restart, like a queued turn. A reply may hold several calls, some that ask and some that do not. Does the turn run the calls it may and pause at the first that asks, or decide them all first? Does it wait in its goroutine, or end and start again when answered?

## The decision

**Every call of a step is decided before any of them runs.** All the calls that ask get their rows and their cards at once, and nothing of the step runs until each is answered, the turn is stopped, or `agents.approval_timeout` passes.

**The turn waits where it is**, in the goroutine that works its chat's queue, woken by the approval route or every `chats.stream_poll`. It stays `running`, and it holds its chat's queue: a message sent meanwhile waits behind it.

**A restart finds it from the database.** A running turn whose newest message is the reply that made the calls, with rows in `tool_approvals` for that reply, goes back in the queue instead of being closed as interrupted. When it runs it skips writing the person's message, makes the step's knowledge lookups again, decides the step again (an answer already given stands), and waits again.

## Why

- With nothing run before the wait, a stop or a restart during it leaves nothing half done: no result to keep, no call to remember. Going on is "answer this reply's calls", read from rows that already exist.
- Pausing at the first call that asks would run the calls before it and leave their results to be kept somewhere while the turn waits. It would also let a call after the pause be decided on a state that changed under it.
- Waiting in place needs no new turn status, no change to the turn's shape, and no new state for the stream: the stream stays open, with heartbeats, and the screen shows the card from the event. A turn that ended and was started again by the answer would need both.
- A turn holds its chat's queue already while it runs. A later message usually depends on what the earlier one did, so waiting behind it is right.

## What it costs

- A read-only call in the same reply as one that asks waits too.
- The chat's queue is held while the person decides. The stop button ends the wait, and an unanswered call expires after `agents.approval_timeout` (24 hours as shipped).
- A goroutine is held for each waiting turn. A person has few.
- Going on after a restart reruns the read-only calls of the step that had already run when the server stopped, if any. A changing call is never run twice (decision 0220).
