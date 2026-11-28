# 0721. The policy is checked when a message is sent and when its turn starts, for every turn

November 28, 2026 · decided in P14-05

## The question

The spec says a turn that names a model the person may not pick is refused, 403 `model_not_allowed`, and taking a chat off Auto when the person may not is refused, 403 `auto_required`. A 403 is an answer to a call. But a turn can wait in the chat's queue while the admin changes the policy, and some turns are sent by code with no call at all: `Runner.Send`, which background tasks, schedules and sub-agents use. Where is the policy checked, and does it hold for those turns?

## The decision

- **When the message is sent.** `POST /api/v1/chats/{id}/turns` checks the policy after the body is checked and before anything is queued, and answers 403 with the code and a sentence for the person. No turn is written. A retry and an edit that name a model are checked the same way. There is no other route that changes a chat's model: a pick goes with a message (decision 0690).
- **When the turn leaves the queue.** `openTurn` checks it again, as the policy stands then, for every turn whatever sent it. A refused turn fails with the same code and sentence, before any model is opened. A turn that has started already (it has a model, and goes on after an approval) is not checked again, and neither is a stronger model (decision 0724).
- A turn that names no model is never refused: while Auto is required it puts its chat on Auto (decision 0722), and otherwise it uses an allowed model (decision 0723).

## Why

- The answer to the call is what the person sees at once, and nothing is written for a message that could never run.
- The check at the start is what makes the server's word hold: a turn queued a moment before the admin's change, or a schedule a person set up naming a model the admin later took away, does not reach that model.
- `Runner.Send` is how unattended work sends turns, so P14-06 gets the policy without code of its own. A model choosing a sub-agent's model is held to the same list as the person, so the list cannot be stepped around by asking for a sub-agent.

## What it costs

- A schedule that names a model the admin has since taken away fails at each run with `model_not_allowed`, and says so, until the person edits it or the admin allows the model.
