# 0240. A picked model goes with the next message, and the server keeps it for the chat

November 25, 2026 · decided in P4-04

## The question

The model picker lets a person choose the model for one chat. The contract has no route that sets a chat's model: a turn may name one in `model`, and `GET /chats/{id}/model` says which model the next turn will use. Where does the choice live, and how often does the page send it?

## The decision

**The server keeps it, and the page sends a pick once.**

- A pick is held in the page, for that chat, until it is sent. The button and the footnote show it at once.
- The next message names it in `model`.
- When that turn ends, the picker reads the chat's model route again and lets the pick go. From then on it shows what the server answers, and messages name no model.
- A pick on the new chat screen moves to the chat its first message makes.
- Nothing about the choice is kept in the browser's storage.

## Why

- **The server already remembers.** A turn that names a model and opens it writes the name on its row as it leaves the queue, and both a turn that names none and the model route use the model of the chat's newest turn that has one (`lastModel` in `internal/turn/store.go`). Sending the pick once is enough, and the choice then follows the chat to every browser and to the command-line client.
- **The server's answer is the truth.** If the named model does not open, or the turn is refused because the chat is held, nothing is written, and the chat stays on its old model. If a held chat's hosted pick is left out of the route and a local fallback answers, the turn records the fallback. Reading the route after the turn shows the person what the server really did, where a pick kept in the browser and sent with every message would go on showing a model that is not answering.
- **No Go change.** The chats lane is working in `internal/turn`. The contract as it stands carries the choice.

## What it costs

- A pick that has not been sent yet is lost on a reload. It is one click to pick again.
- A pick is held in module state, like the replies in `turns.ts` (decision [0151](0151-arriving-replies-live-outside-the-screens.md)), so it is one more thing that lives outside the screens.
- The picker reads the model route on opening a chat, on opening the menu and after every turn, though `GET /chats/{id}` already carries the same answer. Each is a small local call. It keeps the picker to one route and lets it refresh by itself.
- The new chat screen has no route to ask, so it works out the server's choice from the registry and `chats.default_model` in the server's order. If that order changes in `internal/turn/model.go`, the screen must change with it, until the server answers the new chat's choice itself.
