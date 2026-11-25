# P3-04. Anthropic Messages adapter

Status: doing (models)
Lane: models
Depends on: P3-02
Owner gate: An Anthropic key, for the live check only.
Built without the gate: Everything else: recorded-exchange tests need no key.
Scope: Streaming, tools, thinking, cache control, token counting, errors and retries. Usable for Ollama and LiteLLM endpoints that speak it.
Proof: Recorded-exchange tests pass; an optional live check passes with a key.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What will change:

- A new package, `internal/adapters/anthropic`: a `seam.Provider` that speaks the Anthropic Messages API over HTTP with the standard library and no SDK. It sends the `anthropic-version: 2023-06-01` header, which is a setting, because the gateways that speak this API speak that version.
  - Request: the seam's system blocks, messages, tools, output limit and thinking become one `POST {endpoint}/v1/messages` with `stream: true`. The entry's `provider_model` is sent, and the entry's `output_limit` when the request gives none.
  - Reply: server-sent events become seam events: text, thinking with its signature, withheld thinking, tool calls with streamed input, the stop reason and the four token counts.
  - Thinking: adaptive by default, and a token budget for the model names a setting lists, since older models take only that form.
  - Cache control: the adapter places the marks itself, at the end of the system blocks and on the last block of the last message. The seam has no cache marker yet (P5-03).
  - Token counting: `CountTokens`, a method beside `Stream`, calls `/v1/messages/count_tokens`.
  - Errors: the HTTP status and the provider's error type become one of the seam's kinds. The key is taken out of every message.
  - Retries with backoff, for a call that fails before any event has been passed on. `retry-after` is honoured up to the longest wait.
- A builder for `models.AdapterAnthropic`, in a file of its own in `internal/adapters`, with the settings read there and passed in.
- Settings in `settings/models.json` for the version header, the retries, the waits, the cache marks and the thinking form.
- The folder on the `offline` check's list, and a surface in the `no keys in output` check.

How it will be proven, with no key and no network:

- Exchanges under `fixtures/anthropic/`, with invented content, replayed by an `httptest` server on this machine. Each gives the seam request, the body the adapter must send, what the provider answers, and the reply or the error the seam must see.
- Unit tests for the event reader, the error table, the retry waits, cancellation and the key never appearing.

The owner gate: a live call to the real API needs a key. The item ends `provisional` on that.

