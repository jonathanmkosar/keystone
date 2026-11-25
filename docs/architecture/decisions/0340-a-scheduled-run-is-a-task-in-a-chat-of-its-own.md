# 0340. A scheduled run is a background task that works in a chat of its own, and its reply is its result

November 25, 2026 · decided in P8-03

## The question

The plan says each run of a scheduled task is a chat turn, and the item's proof says runs appear as chats. A run happens with no one watching, so it needs what a background task has: no client, a budget, approvals that wait, recovery after a restart. How is a run built: a turn the scheduler sends itself, a task started from a chat, or something new?

## The decision

**A run is a background task (P8-01) that works in a chat made for it.** When a schedule is due, the scheduler makes a chat titled by the schedule and the date, and starts a task in that chat with `jobs.Runner.StartIn`: the task's rounds are that chat's own turns, so its first message is the schedule's prompt and the run is the chat.

**A task in its own chat has its reply as its result.** A task started the usual way works in a chat of its own and writes a note of its result in the chat it came from. A run has no chat it came from: its work chat and its "from" chat are one. So when it is done no note is written, and the round's reply is noted as the task's result; when it is stopped or fails, a note in the chat says why. `StartIn` returns the task already working in a chat instead of starting a second, so the scheduler can start a run again after being cut short.

**The run's model calls are recorded as the actor `schedule`**, under the task's own request id; a run now is recorded as the person, under the call that asked.

## Why

- Background tasks already are durable, unattended chat turns: a row, rounds, recovery after a restart, stop, the budget gate, approvals that wait across a restart, and a screen that draws them. Building runs on them gives all of it for nothing. A second runner would repeat all of it, and the brief said not to build one.
- A run that is a chat is what the plan asks for and what the person expects: it is in the sidebar, it can be opened and continued, the inspector shows its tokens, and a tool that changes something asks there.
- A run started from some other chat would make two chats per run, one holding only the note. One chat holding the prompt and the reply reads as what it is.
- Writing a note under a reply that already says the same thing would say it twice.

## What it costs

- `internal/jobs` gained a file (`inchat.go`) and five lines in `runner.go`: `deliver` passes a done task in its own chat to `deliverInPlace`, and the note's words for one that did not finish say "the steps above".
- A run's task is stored with `started_by` `person`: the table allows only `person` and `tool`, and changing a SQLite check means rebuilding the table, which other lanes' migrations may touch. The schedule's run row says it was the schedule's.
- A run counts towards `agents.task_open_max`, so a person with three tasks open has a due run wait until one ends.
