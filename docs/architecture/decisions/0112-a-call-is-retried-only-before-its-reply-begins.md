# 0112. The Responses adapter retries a call only before any of its reply has been passed on

November 25, 2026 · decided in P3-05

## The question

A call can fail before it starts, part of the way through, or because the provider asks for a wait. The adapter is to retry with backoff. Which failures does it retry itself, and which does it hand to its caller?

## The decision

**The adapter sends a call again only while it has emitted nothing.** That covers a connection that could not be made, a status such as 429, 500 or 503, and a stream that fails before its first block. To make the last one possible, the seam's `start` event is held back until the first block or the stop.

**Once one event has been emitted, the adapter never sends the call again.** The failure ends the stream with an error event, and the error's `Retryable` says whether a new call might work.

**The wait doubles from `responses.retry_base_delay` up to `responses.retry_max_delay`, for at most `responses.max_retries` retries.** When the provider names a wait, that wait is used.

**If the provider names a wait longer than `responses.retry_max_delay`, the call fails at once.** The error is still marked as one that may be retried.

## Why

- The seam's contract is one stream for each call: one start, blocks in order, one stop. A retry after text has been emitted would either repeat the text or need the caller to throw away what it has shown. Only the caller knows which is right, so the choice is left to it.
- A failure before any output is invisible to the caller, and the adapter is the only place that sees `Retry-After`. Retrying there keeps every caller simple.
- Sleeping for as long as a provider asks would hold a person's turn for a minute with nothing on the screen. Routing (P3-08) has a better answer, which is another model, and it can only use it if the error comes back quickly.
- A wait shorter than the provider asked for would be refused again and count against the account.

## What it costs

- The start event arrives a little later than it could, with the first content. Nothing waits on it today.
- A stream that breaks after its first word is not retried here, however short it was. The caller sees the partial text and an error.
- There is no random spread on the waits. With one install and a handful of calls at once that is harmless. A busy install would want it.
- Retries multiply with any retry the caller adds. Routing should count the adapter's retries as one try.
