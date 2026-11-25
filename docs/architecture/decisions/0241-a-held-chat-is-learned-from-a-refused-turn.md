# 0241. The picker learns that a chat is held from a refused turn, and keeps that in the browser

November 25, 2026 · decided in P4-04

## The question

A chat that was sent content from a sensitive space is held to local and trusted models for good (decision [0211](0211-a-chat-sent-sensitive-content-is-held.md)). The picker should show hosted models as not available in such a chat. How does the page know a chat is held?

## The decision

**From the turn the server refuses.** The model route does not say a chat is held: `ModelChoice` has no field for it, its `reason` is set only when there is no model at all, and its `source` says where the model came from. A turn sent to a hosted model in a held chat, with no local or trusted fallback, fails with the code `sensitive`. When a turn of a chat fails, the picker reads it with `GET /chats/{id}/turns/{turn}`, and if the code is `sensitive` it marks the chat.

**The mark is kept in local storage**, as a list of chat ids under `keystone.chats.held`. A marked chat shows every hosted model greyed, with the reason, and a sentence in the menu.

## Why

- It is the only signal the server gives today without a change to Go, and the chats lane is working in `internal/turn`.
- The code `sensitive` comes only from a chat that is held, or whose record could not be read, which the server treats as held. So a mark is never wrong.
- A held chat stays held, so a mark that lasts is right. Keeping it only in memory would offer the person hosted models again after every reload, and each would be refused again.
- The turn's code is read from the record, not from the words shown under the reply, so a change of wording breaks nothing.

## What it costs

- The page learns it only after one turn was refused. Until then a held chat offers its hosted models like any other.
- It is per browser. Another browser, or the same one with its storage cleared, learns it again from its own refused turn.
- A hosted pick in a held chat that a local fallback answered is not refused, so it marks nothing. The picker then shows the fallback the server recorded, which is true, without saying why.
- The list of ids grows by one for each held chat and is never pruned, also when a chat is deleted. An id is sixteen characters, so it stays small.
- All three go away when `ModelChoice` says the chat is held, which `web.md` lists for the chats lane. The local storage can then be dropped.
