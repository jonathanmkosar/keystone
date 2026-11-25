# 0273. The inspector is a panel opened from a reply, and Pin is beside Copy on every message

November 25, 2026 · decided in P5-04, provisional on the inspector mockup (P1-06)

## The question

There is no inspector mockup. `MainDark.html` has an inspector button in the chat header, and the owner question on pins (chats 004) recommends a small Pin action beside Copy on every message. Where does the inspector open, what does it show first, and where are pinning and leaving out offered?

## The decision

**Inspect sits beside Copy on every reply,** and opens the panel on the turn that wrote that reply. **Pin sits beside Copy on every message,** the person's too, under the bubble, shown on hover and focus and always once pinned. A reply's pin goes on its last message with text.

**The panel is on the right,** over the conversation, 440 pixels wide or the whole screen on a phone. It reads the inspect route when it opens and again when a turn of the chat ends. It shows, from the top: the turn in four numbers (tokens in, tokens out, cost, model calls), what the next message leaves out with Put back, each chat call with how full it was and every part that held something, the turn's other calls, and what recall found.

**Leave out next turn is on each recalled item and each pinned message,** and Unpin on each pinned message. The summary opens to its text, read only.

**Which turn is open, and a chat's pins, are kept outside the screens,** in `Inspector.tsx` and `pins.ts`, as the approval cards are, so the actions and the panel agree.

## Why

- A turn is what the inspector is about, and a reply is where a person sees a turn. A header button would have to guess which turn, and a long chat has many.
- A side panel keeps the conversation in view, so a person can read a reply and what went into it side by side.
- Totals first answer the most common question, what did this cost, before the detail.

## What it costs

- The header button of the mockup is not built.
- Every sentence and layout choice in the panel is a guess until the owner approves a mockup.
- A reply with no text and no turn id (an old message no turn wrote) has no Inspect.
