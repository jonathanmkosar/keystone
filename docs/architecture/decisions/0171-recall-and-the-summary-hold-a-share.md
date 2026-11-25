# 0171. Recall and the summary hold a share of the room ahead of earlier turns

November 25, 2026 · decided in P5-01

## The question

The plan's order of priority puts recent turns ahead of recalled knowledge, and recalled knowledge ahead of the summary of older history. Read strictly, a long chat fills all the room with its own turns, and recall and the summary get nothing in exactly the chats that need them. How is the order kept without that?

## The decision

**The parts that are never left out go in first**: the standing start (rules, profile, person map, project, style), the tools, pinned items and the current turn. What is left is the open room.

**Each part after that has a share of the open room, from a setting.** Attachments take up to `chats.context_attached_percent`, and come before earlier turns. Recall and the summary are each *held* up to their share (`chats.context_recall_percent`, `chats.context_summary_percent`) or what they need, whichever is less, and earlier turns are packed in the rest. Room a part does not use goes to the turns.

**A share of zero leaves the part out.** The two shares for recall and the summary cannot be more than 100 together.

**The standing start has no share.** It is whatever the items that fill it supply. A profile or a project too large for a small model shows in the record as a current turn that does not fit, and is the filling item's to keep small.

## Why

- The summary stands in for the turns that were left out. If the turns could take its room, it would be dropped at the moment it is first needed.
- Recall is the plan's answer to "search before answering anything about the person's world". It has to survive a long chat.
- Shares of what is left, and not of the window, mean a large current message shrinks everything behind it in proportion, and the earlier turns always keep the larger part at the shipped values.
- The order still decides what goes when room is very short: with no open room, everything but the required parts is left out together.

## What it costs

- At the shipped values a long chat gives up to a quarter of its open room to recall and the summary when both are full. Both shares are settings a person can lower.
- The shares are guesses until recall (P6-05) and compaction (P5-02) exist. Those items should move the defaults when they have real sizes to go by.
- The standing start is trusted. Nothing here stops a later item from filling it past what a small model can hold.
