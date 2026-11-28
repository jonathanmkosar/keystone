# 0722. A chat is on Auto whenever Auto is required

November 28, 2026 · decided in P14-05

## The question

`POST /api/v1/chats` takes no body: there is no model to refuse when a chat is made. Yet a chat can be off Auto while the person may not leave it: one made with `router.auto_default` off, one made before the admin's change (decision 0690 keeps existing chats where they are), or one made by a route that makes chats off Auto (a handoff, an incognito chat, a project's chat). What happens to those?

## The decision

- A chat made by `POST /api/v1/chats` while Auto is required is stored on Auto, whatever `router.auto_default` says.
- Any chat off Auto is offered as on Auto while Auto is required: its `ModelChoice.auto` is true and the picker shows Auto.
- Its next turn that names no model puts it on Auto in the record (`SetChatAuto`) and is routed. A turn that names a model is refused (decision 0721).
- When the admin lets the person switch Auto off again, the chats stay on Auto until the person picks a model in one.

## Why

- "May not take a chat off Auto" is about every chat the person sends to, not only the ones made after the setting changed.
- Moving the chat on its next turn, rather than every chat at once when the setting changes, needs no hook in the settings store and touches only chats the person uses.

## What it costs

- A person who may not switch off sees chats they wrote on a picked model answered by Auto from their next message.
