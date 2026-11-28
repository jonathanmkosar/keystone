# 0696. Only a turn on Auto sends a route event, and the picker learns a held chat from the server

November 27, 2026 · decided in P14-04

## The question

The spec says a turn's stream starts with a `route` event (auto, the model, the step, the effort, the reason, any notice), and that `ModelChoice.held` replaces the browser's own record of held chats (decision 0241). Should a turn on a picked model send a route event too, and where does the event go in the stream?

## The decision

- A turn on Auto sends `route` when it leaves the queue, after its first `turn` event and before the `turn` event that names its model, and once more if it is routed again (decision 0693). Its data is `TurnRoute`, which the turn (`Turn.route`) and each of its replies (`Message.route`) carry too, so the footnote is there after a reload.
- A turn on a picked model sends no route event and has no route: its `turn` event names its model, as before Auto.
- The picker reads `held` from the model route. The browser's own list of held chats in local storage, learned from a refused turn, is dropped.

## Why

- Every stream Keystone has written, and every test and check that reads one, stays as it was for a chat not on Auto; the event adds nothing there that the `turn` event does not say.
- The server knows whether a chat is held; the browser's record missed a chat held in another browser and was never cleared.

## What it costs

- A client that wants the model of every turn reads it from the `turn` event, and the route only on Auto.
