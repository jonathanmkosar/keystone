# 0524. A held task's report stays in its own chat when the chat it was started from is not held

November 26, 2026 · decided in P8-04

## The question

A research task's own chat can be sent content from a sensitive space while it works, when recall gives it a page, though the chat it was started from never was. Its report then draws on that content. A background task's note marks the chat it lands in as held (decision 0294). A sub-agent's result is kept from a parent that is not held (decision 0483). Which does a report follow?

## The decision

**The sub-agents' rule, `carry.Up`.** When the task's own chat is held and the chat it was started from is not, the report is saved as an artifact of the task's own chat, which is held, and the note in the other chat is Keystone's words alone: the research is done, and its report stays in its own chat because it was sent content from a sensitive space. The summary is not written there, and the chat is not marked.

The same rule holds one step down: a part that was sent sensitive content gives its findings and its sources only to a task whose own chat is held.

A task started from a held chat is held from its first call, with its parts, so its report goes back to that chat as a sensitive artifact.

## Why

- **The chat that asked may reach a model that may not read it.** Holding that chat instead would change which models answer the person's own conversation because of something a background task met, and the person would not have seen it happen.
- **An artifact is read by its chat's model.** A report saved in the chat that asked can be read there with `read_artifact`. In a held chat only models that may read it can.
- **One guard.** `internal/subagents/carry` already decides what passes up, on plain marks, and is checked as a guard of rule 3. Research adds no second rule.

## What it costs

- The person opens the task's own chat to read the report, and it is not beside the conversation they asked in.
- A report is kept back even when every model the asking chat could reach may read it; the chat's route is not known here.
