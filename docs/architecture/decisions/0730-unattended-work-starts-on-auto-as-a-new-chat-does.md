# 0730. Unattended work starts on Auto as a new chat does: when router.auto_default is on and the person has a ladder

November 28, 2026 · decided in P14-06

## The question

The spec says sub-agents, background tasks and scheduled runs start on Auto unless the call that started them names a model. Auto needs a ladder (an empty `router.ladder` means Auto is not offered), and the admin has `router.auto_default`, "a new chat starts on Auto". Does unattended work follow that switch, or does it go on Auto whenever there is a ladder, whatever the switch says? And does it get a setting of its own?

## The decision

- Unattended work that names no model starts on Auto when `router.auto_default` is on and the person's ladder has a step: `turn.Runner.UnattendedAuto`.
- With the switch off, or no ladder, it starts as it did before Auto: on the model the chat it was started from would use next, or for a scheduled run its chat's own model.
- No setting of its own.

## Why

- The chat a piece of unattended work makes is a new chat, and the switch is the admin's one word on whether new chats start on Auto. An admin who turns it off gets picked models everywhere, not an exception hidden in background work.
- As shipped the switch is on, so the spec's line holds as written: with a ladder, every task, run and sub-agent that names no model is on Auto.
- Without a ladder nothing changes at all, which keeps every check and test written before P14-06 as it was.

## What it costs

- An admin who wants people to pick their chat model by hand and unattended work on Auto cannot have both. An owner question if it is ever wanted; a second switch is a line in `settings/models.json`.
