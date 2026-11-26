# 0471. A link shows the chat as it is now, or as it was when it was made

November 26, 2026 · decided in P10-04

## The question

The plan says "a read-only link to one chat". Does the link show the chat as it was when it was shared, or as it is whenever it is opened? Decision 0040 guessed a snapshot copied out of the tenant; the item's brief recommended "as it is now, read-only", with a snapshot if it is simple.

## The decision

Both, chosen for each link when it is made, with the person's `share.default_view` setting choosing when they do not. It ships as **live**.

- **Live** shows the chat's current branch when the link is opened, as the chat screen does: later messages appear, and switching to another branch shows that one.
- **Snapshot** shows the branch up to the last message there was when the link was made. The link's row keeps that one message's id (`upto`); the view walks up from it by each message's parent. Nothing is copied.

The popover offers the two as "The chat as it is when opened" and "The chat as it is now", each with a sentence on what later messages do.

## Why

- A snapshot is simple here because the record never changes a message: an edit or a retry is a new message beside the old, and only a whole chat is deleted, which takes its links with it. One message id is the whole snapshot, so there is no second copy of the chat to keep, to back up, or to forget to delete.
- Live is what the brief recommended and what a person sharing a chat they are still working on wants: the link stays worth sending.
- A snapshot is what a person wants who will go on to write things they would not show: Claude.ai's own shared chats work that way. Offering both, with a plain sentence each, costs one choice in the popover.
- Neither copies data out of the tenant's database, so the refusals (revoked, expired, held, incognito) are read from the live record every time, whatever the link shows.

## What it costs

- A live link shows whatever is said later, to whoever has it. The popover says so, and the person can turn the link off.
- A snapshot of a branch the person later abandoned still shows that branch. That is what was shared.
- A message written while a reply streams appears in a live view once it is stored; a reply arriving at that moment does not, until it is.
