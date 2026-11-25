# 0284. Incognito is a flag on a chat that stops the memory pass, with a route and no setting

November 25, 2026 · decided in P6-06

## The question

The item says the memory pass is skipped in incognito. There is no incognito mode on chats yet; the plan's memory screens (P6-07) have "incognito chats that keep no record and run no memory pass". What is the smallest thing that lets a person keep one chat out of their memory now, without building the rest?

## The decision

**A flag on the chat.** `memory_chats` holds one row for a chat whose flag was ever set, with `incognito`, and goes with its chat. It is a property of the chat, not a setting: it says nothing about any other chat, and it is not in the registry.

**What it does.** An incognito chat's turns queue no memory pass and leave no row. A pass that was waiting when the chat was made incognito is skipped. Making the chat ordinary again does not read the turns it had while incognito. Nothing else changes: the chat is kept, recall still reads the store, and the turn is recorded as every turn is.

**The routes**, beside the chat's other routes: `GET /chats/{id}/memory` returns the flag and the chat's passes; `PUT /chats/{id}/incognito` turns it on and `DELETE` turns it off, as the pin routes do, with no body.

**The flag is owned by the memory package**, and the routes are in `internal/turn/memory.go`, listed in `internal/app`, so the chat record and its shape are not changed.

## Why

- It is what the item needs and no more. The rest of incognito, keeping no record, touches the chat record, recall and the screens, and belongs to P6-07 with the memory screens.
- A setting would be per person, and the plan's incognito is per chat.
- Leaving no row for an incognito turn is the nearest the memory pass can come to "keep no record" today.

## What it costs

- The chat's shape on the chat routes does not say it is incognito; a screen asks `GET /chats/{id}/memory`. When P6-07 builds incognito chats, the flag may move onto the chat itself.
- An incognito chat's messages are still in the chat record, its calls in the audit log, and recall still brings the store into it. The word may lead a person to expect more; the route's summary says what it does.
