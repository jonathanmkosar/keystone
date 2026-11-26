# 0392. Unattended work at the cap or its budget goes on with a local model and says so, or stops when the person says it may not

November 25, 2026 · decided in P8-05

## The question

The plan: at the monthly cap, unattended work falls back to local models or stops, and says so. Decision [0160](0160-a-capped-call-falls-back-to-a-local-model.md) already moves any call the cap refuses to the next model in the person's chain, and the owner's recommended answer is to fall back and name the model that wrote the reply. A background task's budget (P8-01) refuses with the cap's kind, so it falls back the same way. What is missing is the "says so" for work nobody is watching, a way to choose stopping instead, and a place to hook both without changing how a chat behaves.

## The decision

**One new turn option, `Options.Moved`**, told when a route moves past a model and before the next is tried, which may end the call instead. `internal/app` sets it to `unattended.Moved`. It acts only on a turn that is an unattended run's (found by the turn's request id in the run registry, decision [0393](0393-every-unattended-run-is-registered-with-its-own-budget.md)), and only for a call the cap or the run's budget refused, with nothing sent.

- **`agents.unattended_fallback` on, as shipped**: the call goes on, and a notice is kept. After the round, the run's chat is told in a note: the refusal with its amounts, then which model wrote the reply in place of which. The note a task writes when it ends says it too. A notice is told only when the model it went on to did write in that turn.
- **Off**: the call ends with a `spend_cap` error that says unattended work may not go on with another model, so the run stops and its end note says why.
- **Any other turn is untouched**: a chat at the cap falls back as 0160 has it, whatever this setting says.

The audit log records it with no new kind of entry: the refused call is a model call with the outcome `refused` and the refusal as what came back, and the model that took over has its own entries under the same request id. When the call is ended, the next model is never started.

## Why

- The hook sits where the decision is made: the chain knows a model was refused and which comes next, and asking there lets the call be ended before anything is sent to the next model. A gate asked only for paid models (the budget's) could not stop a local model behind it.
- Notes in the run's chat are what a person reads when they look at the work later; the task's own end note is what they read in the chat they started it from. Both name the model, as the owner's answer asks.
- A setting per person, defaulting to fall back, follows the owner's recommendation and 0160's reasoning (a person who put a local model in the chain has said it may stand in), while letting someone who would rather be stopped say so for the work they do not watch.
- No new audit kind keeps the audit package, which enforces a fixed rule, unchanged.

## What it costs

- One changed line in `internal/turn/route.go`, and one option.
- A fallback to a paid model further down the chain is also told, once it wrote; a person who wants only local stand-ins keeps paid models out of the chain.
- A notice from a call made beside a round after it ended (the memory pass) is kept but not written in the chat.
