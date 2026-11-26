# 0353. A change to an event names its title and guests, and is checked against the event before it is made

November 25, 2026 · decided in P12-02

## The question

A change to an event the person alone is on follows their policy; a change to an event with guests tells the guests and must always ask (decision 0351). The rule decides from the call's input, before the call runs, and the approval card is written from the same input. But whether an event has guests is a fact about the event, not the input: a model, or text it read, could call `calendar_update_event` with an event id alone, which would look personal, run under `always`, and tell every guest. How is a change to an event with guests made to ask, and the card made to show who is told?

## The decision

**`calendar_update_event` and `calendar_delete_event` take the event's current `title` and its `guests`**: everyone the change reaches besides the person, each guest and the organizer when it is someone else, as `calendar_get_event` lists them. `ReachesOthers` reads `guests`, `add_guests` and `remove_guests` from the input, so a call that names anyone always asks, and the card lists them.

**Before it changes anything, the call reads the event and checks both.** A title that differs, or guests that differ from the event's (as a set, without regard to case), refuse the call with nothing changed, and the refusal names the guests to give, so the model can call again, and that call asks. The change is then sent with the event's etag in `If-Match`, so an event that changed since it was read is not overwritten, and with `sendUpdates=all` when anyone is reached and `none` when nobody is.

## Why

- The check makes the card's list a promise: a change runs only when the people it reaches are the people the input named, and the input is what the card showed and what the rule decided on. A call that left guests out to run under `always` changes nothing.
- Reading the event while deciding would put a network call inside the rule, which reads no network and no setting, and the event could still change between the decision and the run. Checking where the change is made, against the live event, closes both.
- The title check means a card that says "Delete “Dentist”" deletes the dentist and not whatever event the id happens to be.

## What it costs

- A change to an event with guests usually takes the model a read first, or a refused call and a second one.
- Guests who are on the event but were not named cannot be reached by mistake; a guest added on Google's side between the read and the change makes the call refuse, by the etag or the check, and the model reads the event again.
