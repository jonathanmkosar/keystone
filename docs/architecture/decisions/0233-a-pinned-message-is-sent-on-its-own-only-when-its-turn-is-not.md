# 0233. A pinned message is held its room, and sent on its own only when its turn is not

November 25, 2026 · decided in P5-02

## The question

The plan says pinned items are never dropped, and the context builder already keeps a pinned attachment or recalled item. A person can now pin a message of the chat. A message lives inside a turn, and turns are kept or left out whole, and may be summarised. How is a pinned message kept, without sending it twice?

## The decision

**The context builder has a part for them, `pinned`.** The turn gives it each pinned message on the branch before the current turn, with the message's place in the call's messages and its text under the line `chats.context_pinned_heading`.

**Room for every pinned message is held first,** with the parts that are never left out. Then the turns are packed. **A pinned message whose turn was sent is there already:** its room is given back, for recall and the summary, and it is not sent again. **One whose turn was left out or summarised is sent on its own** in the `pinned` part, after the summary and before the note.

**A pin too large for the window** makes the call too long, as a message too large for it does, and the budget refuses it. Nothing cuts a pinned message.

**Pins are rows of the chat record,** `chat_pins`, with `chat.Pin` and `chat.Pins` and three routes. A message on any branch may be pinned; a call carries the pins on its own branch.

## Why

- A pinned message the model saw twice would read as two messages. Holding the room first and giving it back is the one order that never sends it twice and never leaves it out.
- Holding room for a pin whose turn is then sent costs the turns a little room in that call and nothing else: the room goes on to recall and the summary.
- A summary may mention a pinned message, but only its own words survive summarising whole, which is what pinning promises.

## What it costs

- A pinned message cannot be left out to make room, so pinning a very long message can make a small model's calls refuse. The record shows it as over, and taking the pin off mends it.
- Only a message's text is sent when it is on its own. An image or a tool result in a pinned message is not.
- The turn inspector (P5-04) will pin and remove items for the next turn. It can pin messages through the same rows.
