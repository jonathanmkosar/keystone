# 0362. A handoff is a new chat with its parent attached

November 25, 2026 · decided in P5-06

## The question

The plan: "Handoff. A button opens a fresh chat carrying a summary of this one, linked back." Should the handoff copy a summary into the new chat, as its first message or as text kept with it, or carry the parent by reference? When is the summary made? What does the new chat start on?

## The decision

**A handoff is an attachment with a mark.** `POST /chats/{id}/handoff` makes a fresh chat and one row in `chat_attachments` with `handoff = 1`: the new chat carries its parent as it carries any attached chat, by its recap, with `open_attached_chat` for the rest. The row is the link both ways: the new chat's `parent`, and the parent's `handoffs`. It cannot be taken off by hand, and goes when either chat is deleted.

**The parent comes first and is never left out for room.** Its item is pinned in the context builder and says the chat continues from it.

**The summary is made at the new chat's first turn**, when the parent has no recap or has moved on, by the same code as for any attached chat (decision [0360](0360-an-attached-chat-comes-in-as-its-recap-and-the-rest-through-a-tool-made-for-each-call.md)). The route answers at once.

**The new chat goes on with the parent's model.** The answer names the model the parent's next turn would use; the screen sends it with the first message.

A chat with no messages has nothing to carry and is refused.

## Why

- One mechanism for both features: the same rule-3 answer, the same tool, the same inspector line, the same tests. A copied summary would be a second kind of carried text with its own rules.
- By reference, the new chat sees the parent's recap as it is now: if the person goes back to the parent, the handoff's next turn carries the newer recap.
- A summary as the first message would be a message the person did not write, in the chat record, and would be sent again in every turn as history rather than in the part that caches.
- Answering at once keeps the button quick; the wait, when there is one, is a summary call in the first turn, which the person is waiting on anyway.

## What it costs

- The new chat depends on the parent: deleting the parent takes the link and the carried summary with it.
- A parent that moves on after the handoff changes what the handoff carries. That is wanted for a chat the person goes back to, and costs a new summary call when it happens.
