# Anthropic Messages exchanges

Test data. Everything in this folder is invented: the questions, the answers, the tools, the ids, the signatures and the token counts. No file holds a key. Each file says so itself with `"test_data": true`.

These are the exchanges the Anthropic adapter is proven on. Each one is a call as it crosses the wire: what Keystone asks, the body the adapter must send, what the endpoint answers, and what Keystone must see. A server on this machine plays the endpoint. The adapter is described in [`docs/architecture/adapter-anthropic.md`](../../docs/architecture/adapter-anthropic.md).

They were written by hand from the published format of the Messages API. None was captured from a live call, because no key was to hand. The live check in the adapter's doc is what proves the format against the real API.

| File | Holds |
| --- | --- |
| `plain.json` | Text answers, the cache marks and the cache counts, the output limit, a reply the provider declines, and an image, a PDF and a text file. |
| `tools.json` | A tool call with its input in pieces, the result going back, two calls in one reply, a failed result, and blocks and events the adapter reads past. |
| `thinking.json` | Adaptive thinking, the token budget form, a model that cannot think, and withheld and redacted thinking going out and coming back. |
| `failures.json` | Every error kind, the retries and their waits, an error inside a stream, a reply that breaks off, and requests refused before any call. |
| `gateway.json` | A local gateway with no key, an address that ends in `/v1`, events with no names, and an error that is not in the API's shape. |
| `cache.json` | The marks a request's cache points ask for (P5-03): the end of the standing start, of the system blocks, of the history and of the last message; an empty start that marks the last tool; a history that ends in thinking; a message left out before the history mark; the setting off. |

## An exchange

| Field | Meaning |
| --- | --- |
| `name` | What the exchange shows. |
| `request` | The seam request. |
| `sent` | The JSON body the adapter must send. Left out when the exchange is about the answer only. |
| `answers` | What the endpoint says to each call in turn: a `status`, `headers`, and either `events` (a stream), `body` (JSON) or `text`. |
| `reply` or `error` | The seam response the stream must add up to, or the error's `kind`, `retryable` and a part of its message. |
| `attempts`, `waits` | How many calls must reach the endpoint, and the waits between them. |
| `partial` | The text that must have arrived before a failure. |
| `model`, `options`, `bare_events` | Changes to the entry, to the adapter's options, and events sent with no `event:` line. |

Run `go test ./internal/adapters/anthropic` after a change. Use invented content only.
