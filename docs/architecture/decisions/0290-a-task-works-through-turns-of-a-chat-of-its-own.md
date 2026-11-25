# 0290. A background task works through turns of a chat of its own, in rounds

November 25, 2026 · decided in P8-01

## The question

A background task must run the tool loop with no browser, survive a restart, ask for approvals, report its progress and be stoppable. Turns already do all of that, as durable rows with a queue and restart recovery. Should a task be a runner of its own beside the turns, or be built on them? And if on turns, in which chat do they run?

## The decision

**A task is built on turns.** Every model call and tool call it makes is a turn's, run by the turn runner. What the task keeps is its own row and its events: which chats it belongs to, where it is, what it may spend, and its result.

**Its turns run in a chat of its own**, its work chat, made when the task starts and titled "Background task: …". The first turn's message is the task's words.

**A task is rounds.** A round is one turn. When a round ends at the turn's step limit the task sends another, with `agents.task_continue_text`, up to `agents.task_max_rounds`.

**The rounds are found from the turns.** They are the turns of the work chat sent under the task's request id, so where a task is, and how its last round ended, is read from the turn rows, never from memory.

## Why

- A second runner would need its own model calls, tool loop, approval waits, audit entries, stop and recovery, and every later change to turns would need making twice. On turns, all of it comes for free, and a task's steps are drawn in a chat by the screen that already draws them.
- Running the task in the chat it was started from would hold that chat's queue for as long as the task runs: the person could not carry on talking there, which is the point of a background task. A chat of its own leaves it free, gives the task a context of its own (as a sub-agent has), and is a place the person can open to watch the steps and answer approval cards.
- A turn's step limit is a guard for one reply. A task needs more steps than one reply, and rounds give it more while each round stays a turn the person could have sent.
- Reading the rounds from the turns means a crash between sending a round and noting it on the task loses nothing: the task finds the round anyway.

## What it costs

- Each task adds a chat to the person's list. It is named as a task, and it is where the task's work can be read.
- The work chat does not see the chat the task was started from. The task's words must stand alone, and the tool's description says so to the model.
- A round's message, after the first, is Keystone's (`continue` or `resume` text), drawn as the person's message in the work chat.
