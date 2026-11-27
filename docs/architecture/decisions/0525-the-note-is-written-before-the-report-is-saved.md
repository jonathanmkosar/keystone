# 0525. The note is written before the report is saved

November 26, 2026 · decided in P8-04

## The question

When a research task ends, two things go to the chat it was started from: a note with the summary, and the report as an artifact. The report is made from what the parts read on the web, which is outside content, and fixed rule 2 says every side effect after outside content asks. The artifact tools read Keystone's own content and do not mark a chat (decision 0420). Which is written first?

## The decision

**The note, then the artifact.** The note is written by `turn.Runner.Note`, which holds the chat's queue, waits for a turn that is running there to end, and marks the chat as having read outside content before the message is stored. Only when it has returned is the report saved as an artifact of that chat. The note names the artifact by its title, since the artifact has no id yet.

A report that could not be saved is tried again at the next start, and an artifact already written for the task's request id is not written twice.

## Why

- **The other order leaves a gap.** With the artifact saved first, a turn already running in that chat, which the note must wait for, could read the report with `read_artifact`, which marks nothing, and then run a tool that changes something without asking. The wait can be minutes when the person is in the middle of a long reply.
- **A fixed rule has no gaps.** The mark must be on the chat before anything from the web can be read in it.
- **The queue is the place for it.** Marks written with the queue held are on the chat before its next turn, which is how a task's note and a sub-agent's result already do it.

## What it costs

- For a moment the note says the report is saved and it is not. A failure to save it leaves that note standing until the next start saves it; the task's row has no artifact meanwhile, and the screen says so.
- The note cannot carry the artifact's id. A model reads the chat's artifacts by title, or lists them.
