# 0732. Auto is decided when the work starts and kept on its chat, with no column of its own

November 28, 2026 · decided in P14-06

## The question

Where is "this work runs on Auto" kept: a new column on `jobs` and `subagents`, read before each round, or the chat's own `auto` that P14-04 added? And is it decided once, or again at every round?

## The decision

- It is decided once, when the work starts, and kept as the chat's `auto` (`turn.SetChatAuto`, as decision 0690 foresaw). The work's turns then name no model, so the turn runner routes each one, as it routes a person's message in a chat on Auto.
- Work that names a model keeps its chat off Auto, and its turns name the model, as before.
- No migration. `jobs.model` and `subagents.model` stay empty for work on Auto.

## Why

- The turn already reads the chat's `auto` before every turn and does everything else: the verdict row, the route on the turn and its replies, the route event, the effort on every call, a resumed turn reading its route back. A second record could only disagree with it.
- Deciding at the start matches a person's chat: it is put on Auto when it is made, and stays so until something changes it.

## What it costs

- A ladder emptied while a task runs leaves its chat on Auto with nothing to route by: its next round uses the chat's own model, not the parent chat's model it would have had before Auto. A ladder set while a task runs does not move it onto Auto.
- The API shows a sub-agent on Auto with `model` empty, as it showed "the parent chat's" before; the route of its turn names the step.
