# 0272. A removed item is marked on the builder's input, recorded, and never moves a call

November 25, 2026 · decided in P5-04

## The question

An item the person took out of a turn must be left out of each of its calls. The turn could drop it before the context builder sees it, or the builder could be told it was removed. And a removed item may be the reason a chat is held to local and trusted models: should taking it out change where the call may go?

## The decision

**The builder is told.** `contextbuild.Item` gains `Ref`, the inspector's name for the item (`page:7`, `source:3`, `message:42`), and `Removed`. A removed item is sent nowhere and held no room, even when it is pinned, and the record lists it with `removed` and the tokens it would have taken. A part counts its removed items apart from the kept and the dropped, and a part whose items were all removed reads as left out. A removed pin whose turn is sent is in the call all the same, in its turn, and the record says so with `in_turn`. The summary part carries the summary's id, `Input.SummaryRef`, so the inspector can show the text that was sent.

**The turn marks them,** with `inspector.LeaveOut`, after recall and the pins are made for the call and before the builder packs it. Recall's items take their names from the hits recall kept, in order; pins from their message ids.

**Removing never changes where a call goes.** The route is chosen before any call is built, from what recall found, and the chat's sensitive mark is written from it. Neither reads the skips.

## Why

- The record is what the inspector shows and is the request, rebuilt from the input in the builder's tests. An item filtered out before the builder would vanish from the next turn's inspector; marked, it stays there as "left out by you", with what it would have cost.
- Holding a removed pin no room is what the person asked for: the room goes to the earlier turns of that call.
- Fixed rule 3 has two guards, what a call is given and where it may go. Taking an item out only gives less, so it cannot weaken the first. Letting it change the second would add a path by which the rule's answer depends on a person's click; keeping the route as recall decided keeps both guards exactly as they were.

## What it costs

- A chat held because recall found a sensitive page stays held even when the person took that page out of the turn.
- A removed recalled item leaves a gap: the next best hit does not take its place, because recall's limit was applied before the person's choice. The inspector shows the item as removed, not as replaced.
- `Ref` and `Removed` are two more fields on the builder's input and record, which every later slot that holds items (attachments, P5-06) can use as they are.
