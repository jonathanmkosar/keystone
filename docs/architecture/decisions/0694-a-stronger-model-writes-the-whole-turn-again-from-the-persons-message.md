# 0694. A stronger model writes the whole turn again, from the person's message

November 27, 2026 · decided in P14-04

## The question

`POST /chats/{id}/turns/{turn}/stronger` "writes that turn again, on a new branch of the message tree, one step above the step that answered". A turn with tool steps is several assistant messages; its reply message (`Turn.ReplyMessageID`) is the last, under the last tool results. Which message is made again, and what is "the step that answered"?

## The decision

- The turn's first reply is made again, as a retry of it (`retry_of`): the new turn's first reply is a sibling of the old one under the person's message, so the whole turn, tool steps and all, is written again by the stronger model on a branch of its own (decision 0020).
- The step that answered is the turn's step (`Turn.Model`, the model the router chose). The stronger model is `router.Stronger`: the first candidate above it, at the turn's effort, with the facts of the message as they are now.
- The route answers `409 not_auto` for a turn not on Auto or not ended, and `409 no_stronger` when no step above may take the message. It asks `router.Stronger` before the turn is queued, so a refusal is the answer and not a failed turn; the turn asks again when it runs.
- The verdict of the turn asked about is marked `stronger_asked`. The new turn's own verdict row says `rules_only` with the reason `stronger_asked`, and the turn keeps `stronger_of`. It is not routed again if its local step does not answer.

## Why

- Making only the last message again would keep the small model's tool calls and thinking above the stronger model's words, which is the opposite of what was asked.

## What it costs

- The tools of the turn run again, and any that asks for approval asks again.
