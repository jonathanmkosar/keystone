# 0282. The memory pass is a queue in the tenant's database, and a pass a stop left running is not run again

November 25, 2026 · decided in P6-06

## The question

The memory pass runs after a turn and must never delay the reply. Compaction runs a job beside the turn and simply skips a chat whose job is still running (decision [0230](0230-a-summary-is-made-beside-the-turn.md)). The memory pass cannot skip: a person who sends the next message while a local model is still reading the last one would lose that turn's facts. And a pass may be cut short by a restart. How is it run, and what survives a stop?

## The decision

**A queue in the tenant's database.** When a turn ends as `done`, one row for it is written to `memory_passes`, `queued`, and the chat's memory worker starts beside the turn, in a goroutine of its own with a store of its own. In one process a chat has one worker, claimed in the runner's memory. It takes the chat's passes oldest first, each `running` until it ends as `done`, `skipped` or `failed` with its reason. When none waits it lets go, looks once more, and takes the chat back if a pass came in as it let go, so none is missed.

**One line where the turn ends.** `runTurn` calls `memoryLater` after it closes the turn. Everything else is in `internal/turn/memory.go` and `internal/knowledge/memory`.

**After a stop.** A pass left `running` is marked `interrupted` by the chat's next worker, with a line in the log, and is not run again: it may already have added its source, and a store cannot yet take one back. A pass left `queued` runs when the chat's next turn starts a worker.

**The runner waits for the worker when it closes**, as it waits for a turn, and a pass the closing cuts short writes nothing more.

## Why

- A row for each pass is also the record of what the memory pass did with each turn: the facts, the source and where, or why not. The turn inspector and the memory screens can read it, and the `memory pass` check does.
- Turns in a chat are worked one at a time; passes in a chat are too, so two passes never race over the same chat.
- Running a pass again after a stop risks a second source with the same facts, and paying for the call twice. Skipping it with a line in the log loses one turn's facts at most, and says so. The brief allowed either.

## What it costs

- Nothing runs a queued pass when the server starts: it waits for the chat's next turn, and a chat that is never written in again keeps it queued. P8-01's background jobs are the place for a sweep at start.
- The memory of which chats have a worker is the runner's; a second server on the same data folder would run its own workers, which nothing here allows anyway.
