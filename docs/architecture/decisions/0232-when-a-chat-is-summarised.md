# 0232. A chat is summarised past a trigger share, down to a keep share, and whenever a turn is left out

November 25, 2026 · decided in P5-02

## The question

The plan says older turns are compacted past a threshold. Past what, measured how, and how much is summarised each time?

## The decision

**The measure is the room a call had for its earlier turns.** The context builder records it as `TurnRoom`: what was left once the parts that are never left out and attachments were in, and recall and the summary were held their shares (decision [0171](0171-recall-and-the-summary-hold-a-share.md)).

**A chat is summarised when** the earlier turns the summary does not yet stand for take more than `chats.compact_trigger_percent` of that room (80 as shipped), or when any of them was left out.

**The oldest of them are summarised until** the rest take no more than `chats.compact_keep_percent` of the room (50 as shipped), and at least up to the oldest turn that was sent, so the summary and the turns after it leave no gap.

`compact.Plan` is the rule, a plain function of the record's turns and the room.

## Why

- Summarising before turns are left out means the summary is usually there when it is needed, which a summary made beside the turn (decision [0230](0230-a-summary-is-made-beside-the-turn.md)) relies on.
- The gap between the two shares is how far a chat grows between summaries: at the shipped values, about 30 percent of the room. A summary is made in steps of many turns, never for every turn.
- A turn left out is lost to the model whatever the share says, so it is always reason enough.
- The room for earlier turns, and not the window, is what the turns actually compete for. It shrinks with a long message, recall and the summary itself, and the rule follows it.

## What it costs

- The room changes from call to call with recall and the current message, so the moment a chat is summarised is not exact.
- The newest half of the room stays verbatim. A person who wants more verbatim and fewer summaries raises the keep share.
- With `chats.context_recent_turns_max` set, turns left out by count are summarised too, which is the setting's aim but a call the person may not expect.
