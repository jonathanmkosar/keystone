# 0363. A sensitive attached chat is left out, or holds the route, as recall's items do

November 25, 2026 · decided in P5-06

## The question

Fixed rule 3: content from a sensitive space reaches only models marked local or trusted. An attached chat, or the chat a handoff continues from, may hold such content: it was held by recall, or its recap was made while it was. What does a call carry of it, and does attaching it change where the chat's turns may go?

## The decision

**A call is built for the least of its route.** An attached chat that holds sensitive content (`recap.Sensitive`) reaches a call, in the context or through `open_attached_chat`, only when every model the call's route may reach may read such content (`attach.MayShow`, which asks `recap.MayShow`). Otherwise it is left out, and the call is told how many attached chats were left out, with no title or id, and that a local or trusted model is needed. The tool refuses it without naming it.

**The chat it goes into is held first.** When it is given, `recall.Hold` marks the chat the call is in, before the call is made or the tool's result is handed over; if the hold cannot be kept, it is not given. From then on that chat goes only to models that may read sensitive content.

**A sensitive attached chat holds the route when the chat's own model may read it.** `Runner.sensitive` counts it, as it counts recall's sensitive items: the route keeps to models that may read sensitive content, so a chat on a local model with a hosted fallback is carried its attached chat and the fallback is left out. A chat on a hosted model is not moved: its calls leave the attached chat out.

**A handoff from a held chat is not held by itself.** Its first turn on a local or trusted model carries the parent and holds it; on a hosted model the parent is left out.

## Why

- It is the answer recall, the person map, the knowledge lookups and the past-chat search already give: one rule for every way content of another place reaches a call.
- Building for the least of the route means no fallback can receive what the first model was allowed to read.
- Following recall's routing rule means attaching a sensitive chat to a chat on a local model works, rather than being left out because of a fallback the person never meant for it.
- Not holding a handoff at once keeps "a new chat is the way back to a hosted model" (the owner's recommended answer, knowledge 005) true for a handoff the person runs on a hosted model: it simply goes without the parent's summary.

## What it costs

- A hosted model in a chat with a sensitive attached chat answers without it, and says so only as the line tells it.
- A chat that carried a sensitive attached chat once is held for good, as a chat that read a sensitive page is.
