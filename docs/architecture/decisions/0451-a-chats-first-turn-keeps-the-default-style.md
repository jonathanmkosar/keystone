# 0451. A chat's first turn keeps the default style as the chat's own

November 25, 2026 · decided in P9-05

## The question

A person has a default style for new chats, and may choose a style for any chat. When the person changes the default, which chats does it reach?

## The decision

**New chats only.** The default is the tenant setting `styles.default`, naming a style's id, as `chats.default_model` names a model. A chat with no style of its own uses the default, and its first turn writes the style it used into `chat_styles` as the chat's own (`styles.Keep`, which never writes over a style the chat has). From then on the chat keeps that style until the person picks another for it. `GET /chats/{id}/style` says where the style comes from: `chat` or `default`.

A style that is deleted is taken from the chats that used it; they use the default from their next turn and keep it. A default that named a deleted style is cleared, back to the shipped `normal`.

## Why

- The plan keeps the start of every call of a chat the same so a provider can cache it, and the style is in that start. If every chat followed the default, changing it would change the start of every older chat at once, each paying to write its cache again, and an old chat would read differently from the next message on without the person touching it.
- It is the model picker's rule: a chat keeps the model its last turn used, and the default model is for chats that have none (decision 0240). The two controls sit side by side and behave alike.
- Writing the default on the first turn, not when the chat is made, needs no change to the chat record or to every route that makes a chat (new chats, project chats, handoffs, background tasks, scheduled runs, incognito chats): each gets a style the same way, at its first call.

## What it costs

- A chat made before styles existed keeps the default in force at its next turn, so a person who sets a default first and then opens an old chat gives it that default. The picker shows it, and one pick changes it.
- A chat a person opened and never sent to follows the default until its first message, which is what "the default for new chats" means.
