# 0170. A turn is kept whole or left out, and nothing older is sent past a gap

November 25, 2026 · decided in P5-01

## The question

When a chat no longer fits its model's window, something has to go. What is the unit that goes, which one goes first, and what happens when the current message alone is too large?

## The decision

**The unit is a turn.** A turn is a message of the person's and everything up to their next: the reply, and in a turn that used tools the calls, their results and the replies after them. A turn starts at a message of the person's that holds no tool result, and only where no tool call is waiting for its result. A turn is sent whole or not at all. No message is cut short.

**The oldest go first, and the packing stops at the first turn that does not fit.** Turns are taken from the newest back. A smaller, older turn is not sent past one that was left out.

**The current turn is never left out and never cut.** When the standing start, the tools, the pinned items and the current turn are together more than the window allows, the request is returned as it is, the record says by how many tokens it is over, and `budget.Guard` refuses the call as `too_long`.

**The model is told.** When turns were left out, one sentence from a setting is the last system block. An empty setting sends none.

## Why

- Every provider refuses a tool result with no call before it, and most refuse a call with no result. Cutting on turns makes that impossible by construction, with no repair step after packing.
- A request must begin with a message of the person's. Every turn does.
- A conversation with a hole in it reads as if the missing exchange never happened. An answer that refers to it then looks like the model's own mistake. A clean cut at one point is something a sentence can explain.
- Cutting inside the person's own message changes what they asked without telling them. A refusal that says how far over the message is tells them what to do.
- Without the note a model answers "as I said earlier" about text it cannot see, or denies that something was said.

## What it costs

- One very long turn in the middle of a chat ends the packing there, though older, smaller turns would have fitted.
- A turn with a long run of tool calls is one unit. If it outgrows the window while it is the current turn, the call is refused. Trimming old tool results inside a running turn is left to the tool loop (P7-01).
- The note is a system block that appears on the first call that loses a turn, which changes the system text once in a chat's life. Caching (P5-03) may move it.
