# Responses adapter exchanges

Test data. Everything in this folder is invented: the questions, the answers, the tools, the ids, the numbers and the endpoint. Every file says so itself with `"test_data": true`.

These are the exchanges the Responses adapter is proven on. They were **written by hand** from the public description of the OpenAI Responses API. None was captured from a provider, because the build has no key and no network. The live check, which needs both, is what will show whether they match the real thing. [`docs/architecture/adapter-responses.md`](../../docs/architecture/adapter-responses.md) says which parts of the format are sure and which are inferred.

| File | Shows |
| --- | --- |
| `answers.json` | A streamed text answer with cached input, an item that arrives whole, a reply that stops at the output limit, a refusal, and an image and a file sent inline. |
| `tools.json` | Two function calls in one reply, and the next call of the turn with the calls, their results and the reasoning going back. |
| `reasoning.json` | A reasoning summary with encrypted reasoning, and reasoning with no summary. |
| `failures.json` | A refused key, a request that is too long, a refused field, a spent quota, an endpoint that does not stream, a rate limit and a broken connection part of the way through, and a tool call cut by the output limit. |
| `retries.json` | A busy provider that then answers, a reply that fails before it says anything, three failures in a row, and a wait the adapter will not sit through. |

## The shape of a case

- `request` is the call in the seam's own format.
- `capabilities` are the registry entry's. The rest of the entry is the same for every case and is in the test.
- `exchanges` are the calls the endpoint gets, in order. Each has an optional `wire_request`, the body the adapter must send; a `status`, 200 when left out; optional `headers`; and either `events`, sent as server-sent events, or a `body`, sent as JSON.
- `response` is the whole reply that must come out, or `error` is the kind of failure, whether it may be retried, and a piece of its message.
- The adapter must make exactly as many calls as there are exchanges.

Run `go test ./internal/adapters/responses`. Use invented content only. Nothing here may be a real name, address, account or key.
