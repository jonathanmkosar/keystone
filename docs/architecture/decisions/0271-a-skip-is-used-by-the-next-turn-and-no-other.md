# 0271. A skip is used by the next turn, and by no other

November 25, 2026 · decided in P5-04

## The question

The plan says a person can remove items for the next turn from the inspector. What is "the next turn" when a turn makes several model calls, when a turn is waiting in the chat's queue, or when a turn fails before it calls a model? Where is the choice kept, so it survives a reload and a restart?

## The decision

**A skip is a row of the chat's,** `chat_skips`: the kind (`page`, `source` or `message`), the id, the label the inspector gave the item, when it was asked for, and the turn that used it. A chat has at most one waiting skip of one item.

**The turn that builds the next first model call claims every waiting skip of its chat,** in one statement that writes its id on them. Every later call of that turn reads the skips it claimed. No other turn finds them waiting. So the item is out of the whole of that turn, through every tool step, and back in the turn after.

**A turn that fails before it builds a call claims nothing.** The skips wait for the next turn that does.

**A skip can be taken back while it waits,** and stays with its turn once used, so the inspector of that turn can say what was left out.

**Only what the person could see can be taken out:** a page or source that a call of one of the chat's newest fifty was given, named as that call named it, and a message that is pinned now.

## Why

- A recalled item that left a turn's first call and came back in its second would read to the model as new knowledge in the middle of its own work. The person thinks in turns, not in calls.
- Claiming at the first call, not when the person clicks, means a skip made while a turn is running or waiting goes to the turn that has not yet built anything, which is the one the person means by "next".
- Rows with the chat survive a reload, another browser and a restart, and go when the chat is deleted, as pins and summaries do.

## What it costs

- A turn that is refused after its first call is built, as `too_long` or over the cap, has used its skips.
- A skip made during a turn's later steps waits for the turn after, not those steps.
- Taking out a page that the next message does not recall anyway does nothing, and is still used up.
