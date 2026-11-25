# P3-06. Chat Completions adapter

Status: doing (models)
Lane: models
Depends on: P3-02
Owner gate: The Mac mini gateway address and an OpenRouter key, for the live checks only.
Built without the gate: Everything else: recorded-exchange tests need no key.
Scope: Streaming and tools for OpenRouter, LiteLLM, Ollama and the Mac mini gateway, including a custom certificate.
Proof: Recorded-exchange tests pass against each endpoint style; an optional live check passes against Ollama.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What will change:

- A new package, `internal/adapters/chatcompletions`: a `seam.Provider` that speaks the Chat Completions format over HTTP with the standard library only. It sends a streamed request, reads the reply as server-sent events, and turns text, reasoning and tool calls into seam events. It keeps its own small SSE reader and its own retry loop.
- Errors are mapped to the seam's kinds from the HTTP status and the provider's error body. A call is retried with backoff only before anything has been sent to the caller.
- One field added to the registry entry, `ca_cert`: a file path or PEM text naming the one certificate authority to trust for that entry. There is no way to switch verification off.
- The key is the builder's `key` argument. No key means no `Authorization` header. The key is taken out of every error message.
- A builder for `models.AdapterChatCompletions` in a file of its own, `internal/adapters/chatcompletions.go`, which reads the timeouts and retry limits from `settings/models.json`.
- The folder is added to `networkAllowed` in the `offline` check.

How it will be proven, with no key and no network:

- Recorded exchanges under `fixtures/chat-completions/`, invented, one for each endpoint style (OpenRouter, LiteLLM, Ollama, the gateway), replayed by `httptest` servers on this machine. Each fixture holds the seam request, the body the adapter must send, the reply as the server streams it, and the seam response it must add up to.
- A TLS test server whose certificate is made at test time and is trusted only through `ca_cert`: the call fails without it and works with it, from PEM text and from a file.
- Each HTTP status and error body maps to its seam kind; a retry happens after a busy reply and not after a refused key; a stream that breaks off is `unavailable`; cancelling stops the call; the key is in no error.

Not built today, behind the owner gate: live checks against the Mac mini gateway and OpenRouter, and the optional live check against Ollama.

