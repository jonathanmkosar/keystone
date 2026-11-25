# 0291. A task resumes after a restart with a new round, and its note is written once

November 25, 2026 · decided in P8-01

## The question

Decision 0142 closes a turn that was running when the server stopped as failed with `interrupted`, keeping its words, because running it again would pay for the call twice without being asked. A background task must resume after a restart. Should its interrupted turn be run again, or the task go on some other way?

## The decision

**The interrupted turn stays as 0142 leaves it.** The turn runner's recovery runs first and closes it.

**The task then sends a new round**, with `agents.task_resume_text`: the model is told Keystone was restarted while it worked, that its last step was cut short, and to check what was done before doing anything again. The round counts towards `agents.task_max_rounds`.

**A round that waited for an approval** is put back in its chat's queue by the turn runner (0222) and waits again; the task simply follows it.

**The note is written once.** The turn that will write the task's note in the chat it was started from is named on the task before it is written. After a restart a task whose note is not written writes it under that name; a note already written under it is found and kept.

## Why

- The person asked for the task to finish unattended. Going on after a restart is what they asked for, so the call it costs is not unasked, unlike a chat turn the person may no longer want.
- A new round needs no change to how a turn runs or recovers, and the record shows exactly what happened: the cut-short round with its words, then the round that resumed.
- Telling the model it was cut short, and to check before acting, is what keeps a resumed task from doing a step twice. A changing call is never run twice anyway (0220).
- Naming the note's turn first makes writing it safe to repeat.

## What it costs

- A restart costs a task a round, and the model call that was cut short is paid for again in the next round.
- A read-only tool that ran in the cut-short round may run again in the next.
