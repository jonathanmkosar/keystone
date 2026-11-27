# 0634. The fake answers decision calls from scripts marked `"decision": true`, in the same files as the turns

November 27, 2026 · decided in P14-02

## The question

The spec says the fake provider answers decision calls from recorded scripts, as it answers turns (decision 0011). A script today matches a request by its user text, system text and model, and plays steps of replies. How is a decision scripted, without a second file format or a change to how scripts are loaded?

## The decision

- **A script whose `when` has `"decision": true` answers decision calls, and only those.** A script without it answers only turns, so the catch-all script that answers any message answers no decision.
- **The same conditions, read for a decision**: the state is the user text, and the questions' instructions, one to a line, are the system text; the model is the request's.
- **One step**, which is a `decision` (a `seam.DecisionResult`) or an `error`. The answer is checked against the request when it is played, and one that does not answer it is refused as `invalid`, as a real endpoint's would be.
- A turn script and a decision script with the same conditions are two scripts, not a duplicate.

## Why

- `fake.Load`, `fake.Decode` and the fixture folders stay as they are, so every caller that loads scripts gets decisions for nothing.
- The router's tests (P14-03) can script each step of the ladder by the newest message, which is in the state.

## What it costs

- A decision script's answer names the option ids it expects; a test that changes the ladder changes the script.
