# 0160. A call falls back on the endpoint's failures, on its window and on the cap, and never on the request's own

November 25, 2026 · decided in P3-08

## The question

A route is a first model and a chain of others. Which failures of the first are worth sending the same call to the next? And at the monthly cap, the plan says unattended work "falls back to local models or stops". What does a chat do?

## The decision

**A call moves to the next model when the failure is `unavailable`, `overloaded`, `rate_limited`, `auth`, `too_long` or `spend_cap`.** It does not move on `invalid` or `other`.

**The cap is asked before each model of the route, and a model that is local is never held by it.** So a paid call that the cap stops goes to the first local model in the person's fallback chain, if there is one, and fails with `spend_cap` if there is none. This is the same for a chat and for anything else that calls through a chain.

**It moves only before any block of the reply has been passed on**, which is the rule the adapters already keep for their own retries (decisions [0102](0102-an-adapter-retries-only-before-the-first-event.md), [0112](0112-a-call-is-retried-only-before-its-reply-begins.md), [0122](0122-retries-stop-at-the-first-event.md)).

## Why

- The first four are troubles with one endpoint or one key. Another model has another of each.
- `too_long` is the budget's refusal or the provider's. A model further down the chain may have a larger window, and one that does not refuses it again before anything is sent, at no cost.
- `invalid` means the provider read the request and called it malformed. The next provider is sent the same request. Trying it there would hide a bug in Keystone behind a second failure.
- Nothing falls back unless the person named a chain. `routing.fallback_models` ships empty, so nobody gets an answer from a model they did not ask for until they have said which ones may stand in.
- At the cap, a person who put a local model in their chain has already said it may answer for the paid one. Stopping the chat when a free model is named and able would be the less useful reading of the same setting. The reply's own record and the turn's usage event name the model that answered, so it is not hidden.

## What it costs

- A wrong key on the first model is quiet when a fallback answers. It is in the audit log as a failed call and in the server's log, and `tools/tenant model list` shows a key that is missing. A screen that shows a model's health is P11-03.
- A reply at the cap may come from a smaller model than the one picked. A person who would rather be stopped leaves local models out of the chain.
- There is one chain for a tenant, not one for each kind of task.
