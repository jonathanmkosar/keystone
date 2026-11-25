# 0151. Replies that are arriving live outside the screens, and are read with fetch

November 25, 2026 · decided in P4-04

## The question

A reply streams for seconds or minutes. In that time a person may open another chat, start a new one, or lose their connection. Where does the page keep a reply that is arriving, and how does it read the stream?

## The decision

**Where.** In `web/app/src/turns.ts`, a small store that belongs to no screen: one entry for each chat that has a turn this page is following. A screen reads its chat's entry with `useLive` and is told when it changes. The store calls `api.ts` for the stream and folds each event into the entry. It is plain functions and a map, with no state package.

**How.** With `fetch` and a reader for server-sent events written here (`src/sse.ts`), not with the browser's `EventSource`.

**When a turn ends**, the screen reads the chat again from the server and lets the entry go. A failed turn's words are kept until the next message is sent.

## Why

- **A reply outlives the screen that sent it.** The contract says a turn goes on without the client. If the chat screen held the stream, opening another chat would drop it, and coming back would mean reading it again from the first event. In the store, the reply goes on arriving and is there when the person comes back.
- **A new chat changes address in the middle.** The first message is sent from `/`, and the page then moves to `/chats/<id>`. The stream has to belong to something that is still there after the move.
- **`EventSource` cannot do what the contract asks.** The first stream is the answer to a `POST` with a body, and `EventSource` only sends `GET`. It also comes back by itself after a stream closes, on its own timing, and hides the status of a refused stream. The page needs to see a 401 or a 404, to stop after `end`, and to choose when it tries again.
- **The record is the truth.** The stream's `message` events already carry each stored message, so the screen could keep what it built. Reading the chat again when a turn ends costs one call, and it also brings the chat's new place in the list and any turn that is waiting behind this one.

## What it costs

- The store is module state, so it is one more thing to clear at sign-out. `ChatsProvider` does that when it leaves the page.
- Redraws are held to one every 24 milliseconds while text arrives, so a fast reply does not redraw for each piece. That number is a constant in the store, not a setting: it is not something a person would change.
- How long a thinking block took is known only to the page that watched it arrive. After a reload the step says "Thought", with no time. Keeping it would need the server to record it.
- The page follows one turn for each chat. A second message cannot be sent while a reply is arriving, though the server would queue it.
