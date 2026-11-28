# 0690. A chat's Auto is kept on the chat, set when it is made, and changed by the message that picks

November 27, 2026 · decided in P14-04

## The question

The spec gives each chat `auto` (yes or no), a new chat `router.auto_default`, and says picking a named model takes a chat off Auto. It does not say what the chats that already exist are on, when a new chat takes the default, or how a person puts a chat back on Auto.

## The decision

- `chats.auto` is a column of the chats table. The migration leaves every chat that exists off Auto: it keeps the model it was on, as a chat keeps its model when the default model changes (decision 0451).
- `POST /api/v1/chats` puts the chat it makes on Auto when `router.auto_default` is true (the default). Chats Keystone makes itself (handoffs, tasks, schedules, sub-agents, research, the importer) are made off Auto until P14-06 decides what unattended work starts on; `turn.SetChatAuto` is there for it.
- A pick goes with the next message, as a picked model always has (decision 0240): `SendBody.auto` true puts the chat on Auto, a named `model` takes it off, both together are refused. The turn applies it when it leaves the queue, so the messages of one chat change it in their order.
- A chat on Auto with an empty ladder runs on the model picked, exactly as before Auto; `ModelChoice.auto` is then false and the picker offers no Auto. Setting a ladder later puts such a chat on Auto with no other change.

## Why

- The owner's chats were written on the models he picked; moving them to Auto by a migration would change which model answers them without his asking.
- One rule for picks, the one the picker already follows, keeps the browser's pick and the server's record from disagreeing.

## What it costs

- The owner's existing chats stay on their models until he picks Auto in each; new chats start on Auto once he has a ladder.
- `router.may_switch_off` is not enforced yet: P14-05 adds the refusal `auto_required`.
