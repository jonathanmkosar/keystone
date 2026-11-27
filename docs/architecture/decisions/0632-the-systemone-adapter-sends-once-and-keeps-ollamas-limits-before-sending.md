# 0632. The systemone adapter sends a call once, within its timeout, and keeps Ollama's limits on the body before sending

November 27, 2026 · decided in P14-02

## The question

The spec gives the adapter three settings: `systemone.timeout`, `systemone.max_request_bytes` and `systemone.max_questions`, and says a request over either limit is refused before it is sent. What is measured, what kind is a refusal, does a failed call retry as the chat adapters do, and what address is called?

## The decision

- **The size is the request body as sent**: the JSON with the provider's model name, the state, the questions and their options. That is what Ollama counts against its 64 KiB of text and schema. A body exactly at the limit is sent.
- **Both refusals are `too_long`**, with the number found and the limit: a request a smaller one would get past, as the budget's refusal is. The decision package records them as refused. A request the seam refuses (a choice of one option, a blank state) is `invalid`.
- **One attempt, bounded by `systemone.timeout` from connecting to the whole answer.** A late call is `unavailable` and retryable, and says how long it waited. There is no retry in the adapter.
- **The address is `{endpoint}/v1/systemone`**, with the endpoint Ollama's own address. One written with `/v1` on it, as a Chat Completions entry for the same Ollama is, is not given a second `/v1`.
- Ollama's error body `{"error": "..."}` is mapped to the seam's kinds: a 400 about the context is `too_long`, a 404 (the model is not pulled) `invalid`, 413 `too_long`, 503 `overloaded`, other 5xx `unavailable`.

## Why

- A verdict is worth something only quickly: the router's own timeout (2s) is shorter than the adapter's (5s), and the rules decide when it runs out. A retry would only make the wait longer.
- Measuring what is sent is the only number that matches Ollama's own limit; a count of characters would miss the JSON around them.
- Accepting both address shapes saves the person from knowing that Ollama's two APIs are rooted differently.

## What it costs

- A decision model that is still loading can fail the first verdict after it starts, where a chat call would have waited and retried. The router then decides by rules for that one request.
- The 64 KiB default is Ollama's limit for text and schema, which the body slightly overstates, so a request a few bytes under Ollama's limit may be refused here.
