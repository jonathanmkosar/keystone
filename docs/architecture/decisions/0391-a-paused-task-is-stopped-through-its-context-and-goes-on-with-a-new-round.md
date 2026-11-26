# 0391. A paused task is stopped through its context, marked paused, and goes on with a new round

November 25, 2026 · decided in P8-05

## The question

The switch that pauses every agent must stop every running background task within seconds. A task's round is a turn that may be in the middle of a model call, a tool, or a wait for an approval. How is it stopped, what is the task afterwards, what happens to a task started while the switch is on, and what happens when the switch goes off?

## The decision

**The round is stopped as the stop route stops a turn**: the turn's context is cancelled, so the model call ends at once and is recorded as a stopped call, in the audit log (an end entry marked stopped, with what had arrived) and in the usage records (outcome `stopped`), and what it had written is kept in the chat. Fixed rule 4 holds for the cancelled call as for any other.

**The task is marked paused, not failed, and not stopped.** Its stored status stays `queued` or `running`, a new column says the switch holds it and which round it stopped, and the API shows the status `paused` with progress words that say why. A note in the task's chat says its last step was cut short by the pause. It still counts as open (`agents.task_open_max`), and a stop the person asks for wins over the switch.

**A task started while the switch is on is held**: it is written, it is shown as paused with "Held: …", and nothing is sent.

**When the switch goes off, a paused task goes on with a new round**, sent with `agents.task_unpause_text`, as a round a restart cut short goes on with `agents.task_resume_text` (decision [0291](0291-a-task-resumes-with-a-new-round.md)). The model is told it was cut short and to check what was done before doing anything again. A held task starts. The round counts against `agents.task_max_rounds`.

## Why

- Cancelling the context is the one way that ends a provider's call at once; every adapter already honours it, and the turn already records a stopped call fully. Nothing new is needed in the turn.
- Paused is a distinct state because the person did not ask for the task to end: failed would say something went wrong, and stopped would write the task's end note and lose the work to come.
- Keeping the stored status as it was avoids rebuilding the `jobs` table (SQLite cannot change a check in place, and the table has children), and keeps every count of open tasks right with no change.
- A new round is how a task already recovers from a cut-short round, so the pause reuses a path the restart check proves. Going on where the call was cut is not possible: a provider's half reply cannot be continued.
- Holding a new task, rather than refusing it, keeps what the person or the model asked for, and the switch says it waits.

## What it costs

- A paused round's call is paid for, up to the point it was cut, and the new round pays again for the context. The provider may bill for what it wrote before the cancel, which the usage record cannot show (as for any stopped call).
- A tool that changes something and had finished before the cancel is not run again unasked (decision 0220), but a read-only one may be.
- A task paused many times can reach its round limit and fail with `round_limit`.
- An approval card the pause stopped is answered too late: the model asks again in the new round.
