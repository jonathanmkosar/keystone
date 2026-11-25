# 0102. The Anthropic adapter retries a call only before it has passed anything on, and never follows a redirect

November 24, 2026 · decided in P3-04

## The question

The item's scope includes retries with backoff. A provider streams through a callback (decision [0015](0015-a-provider-streams-through-a-callback.md)), so by the time a call fails the receiver may already hold part of the reply. Routing (P3-08) will also retry and fall back. Who retries what?

## The decision

**The adapter retries the same call at the same endpoint, and only while it has passed on no event.** Once the `start` event has gone to the receiver, a failure ends the stream with an `error` event, whatever its kind. The error still says `retryable`, so the caller can decide.

**The provider's wait is honoured up to a limit.** A `retry-after` no longer than `anthropic.retry_max_delay` is waited out. A longer one fails the call at once.

**A redirect is not followed.** A 3xx answer is an `unavailable` error that is not retryable.

**The adapter keeps its own copies of the event reader and the retry code.** Nothing is shared with the other two adapters.

## Why

- A receiver that has shown half an answer cannot be handed the start of a second one. Only the caller knows whether to throw the half away, so the choice after the first event is the caller's.
- Most failures that pass on their own, a rate limit, a busy provider, a refused connection, come before the first event. Retrying there is invisible and needs no decision from anyone.
- Waiting an hour inside one call would hold a turn open for an hour. Failing at once lets routing try another model.
- Go's HTTP client drops the `Authorization` header when a redirect leaves the host. It does not know `x-api-key` is a key, and would send it on. A wrong address in an entry should fail, not move the key somewhere else.
- The three adapters were built at once in separate folders. A shared helper would have been a fourth folder all three waited on.

## What it costs

- A reply that breaks off half way is not retried by anyone until routing or the turn does it.
- Routing must not multiply the adapter's retries without meaning to: two retries here under three tries there is nine calls. Routing can build its providers with `MaxRetries` at zero, or count on these.
- The event reader and the backoff exist three times. If the three copies are the same in practice, the integrator can fold them into one package.
