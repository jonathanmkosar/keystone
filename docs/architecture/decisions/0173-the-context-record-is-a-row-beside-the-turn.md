# 0173. What a call was built from is a row beside the turn, not an event in its stream

November 25, 2026 · decided in P5-01

## The question

The builder returns a record of every part of a call, for the turn inspector (P5-04). A turn can make several model calls, and each has its own record. A turn's events are already rows (decision [0140](0140-a-turn-and-its-events-are-rows.md)). Where is the record kept, and how do the builder's settings reach it?

## The decision

**A table of its own, `turn_contexts`**: the turn, the step, and the record as JSON, one row for each model call. `internal/turn` owns it and `turn.Contexts` reads it. The rows go with the turn when its chat is deleted.

**It is not an event.** Nothing new is written to `turn_events`, and the stream a client reads is unchanged.

**A record that cannot be stored does not stop the call.** The failure is logged. The audit log's entry for the call still holds what was sent.

**The builder's settings are all tenant scope, and are read for each call** through `tenantsettings.Get`, as the turn already reads a person's reply and thinking limits. Nothing was added to `turn.Config`.

## Why

- An event is something the chat screen is sent while a reply streams. The record lists every turn of the branch, which for a long chat is thousands of entries, on every call. The screen has no use for it until the person opens the inspector.
- A new event name would have changed the stream's contract and the OpenAPI document while the chat screen was being built against them.
- The audit log has what was sent, but as one body. The inspector needs it by part, with what was left out, and the audit log does not know what was left out.
- The standing instructions are the person's to change, and a person on a small local model has reason to change the shares. Reading them for each call means a change takes effect on the next message with no restart.
- Keeping the settings out of `turn.Config` kept this item's change to the runner to one place, while another lane worked in the same package.

## What it costs

- One more migration and one more table that grows with use. Like `turn_events`, its rows are removed only with their chat.
- The record of a long chat is large: an entry for every turn of the branch, kept or not. If that shows in the size of a database, the inspector's item can store the dropped turns as a count.
- Eight settings are read from the database for each model call. They are single-row lookups on a primary key.
- An install's admin cannot fix the standing instructions for everyone: each person may change their own. If that is wanted, the setting's scope is the thing to change.
