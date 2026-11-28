# 0693. A local step that does not answer is routed again once, before anything is passed on

November 27, 2026 · decided in P14-04

## The question

The router's fourth rule skips local steps while the local machine is known to be down (`router.local_down_for`), and the turn keeps when it last failed (`Facts.LocalFailedAt`). The first message after the machine goes down does not know: the router chooses a local step and its call fails with `unavailable`. What does that message get, and what counts as the machine failing?

## The decision

- The runner keeps, for each tenant and in memory, the last time a call to a local model failed with `unavailable`: a chat call whose last model was local, a model a fallback chain moved past, or a decision call (`Record.LocalUnavailable`). A restart forgets it.
- When the first call of a turn on Auto fails with `unavailable` on a local model, before any block of its reply was passed on, the turn routes the message again with `LocalFailedAt` set: local steps are skipped, the reply carries the notice `local_unavailable`, and a held chat with no other step fails with the code `local_unavailable`, which may be retried. A second route event says so, and a second verdict row is kept.
- It is done once, and only for the first call. A tool step whose local call fails later fails the turn, as any failed call does: the choice holds for the whole turn, since a thinking block goes back only to the model that wrote it.
- A fallback chain that answers from a later model (`routing.fallback_models`) answers as it always has; the time is still noted, so the next message skips the local steps.

## Why

- The spec's "a chat on Auto is answered by the next step with the notice" holds for the first message as well as the later ones, without waiting for the person to send it again.
- Moving a turn to another model after a tool step has run would send one model's tool calls and thinking to another.

## What it costs

- The first message after the machine goes down waits for one failed call before it is answered.
- Two servers on one data folder would each learn it on their own; Keystone runs one.
