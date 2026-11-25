# 0142. After a restart a queued turn runs, and a turn that was running is closed as failed

November 25, 2026 · decided in P4-03

## The question

The server stops with turns open. Some were waiting. Some were in the middle of a model call, with part of a reply already on the person's screen. What happens to each when the server starts again?

## The decision

**A turn that was waiting runs.** Nothing of it had been sent to a model or shown to anyone.

**A turn that was running is closed as `failed`, with the code `interrupted` and `retryable` set.** It is not sent to the model again.

**What it had streamed is kept.** The text deltas since its last stored message are read from `turn_events` and appended to the chat as an assistant message with the stop reason `failed`. Thinking is not kept, because a thinking block without its signature goes back to no provider.

**Stopping the server on purpose is the same as losing it.** `Runner.Close` writes nothing, so there is one path through a restart and the tests walk the real one.

## Why

- Running it again would send the same request a second time and pay for it a second time, without the person asking. For a turn that had already written most of a long answer, that is the larger part of its cost.
- A second run gives different words. The events already hold the first run's words, and a client that replays the turn would see the two joined.
- The person can see exactly what happened: the half reply, an error that says Keystone stopped, and a retry that is one click.
- The audit log already says the same thing: a `started` entry with nothing after it is a call that never finished.

## What it costs

- A turn that was one second into its call is not picked up without the person. If daily use shows restarts in the middle of turns are common, running such a turn again when it had streamed nothing is a small change in `Recover`.
- One server for each data folder is assumed. A second server starting on the same folder would close the first one's running turns as interrupted.
