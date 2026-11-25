# P3-05. OpenAI Responses adapter

Status: provisional
Lane: models
Depends on: P3-02
Owner gate: An OpenAI key, for the live check only.
Built without the gate: Everything else: recorded-exchange tests need no key.
Scope: Streaming, tools, reasoning, errors and retries.
Proof: Recorded-exchange tests pass; an optional live check passes with a key.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/adapters/responses`: a `seam.Provider` for one registry entry that speaks the OpenAI Responses API. Standard library only, no SDK. It posts to the entry's endpoint with `/responses` added, and reads the reply as server-sent events.
- The request: system blocks become `instructions`, messages become input items (text, images and files inline, function calls, function call outputs, reasoning items), tools become function tools, and the output limit goes across. Nothing is stored at the provider (`store` is false), so reasoning comes back encrypted and is kept in the thinking block's signature, to go back to the same provider unchanged.
- The stream: the provider's events are folded into the seam's start, block and stop events, one block open at a time. Usage is split so the seam's counts do not overlap.
- Errors: an HTTP status, an `error` event and a failed response each become a `seam.Error` with a kind and whether it may be retried. A stream that breaks off before its end is a failure.
- Retries: a call that failed before any event was passed on is sent again, with a wait that doubles and honours `Retry-After`. Once an event has been passed on, nothing is sent twice.
- The key is the builder's `key` argument and goes in the `Authorization` header only. It is taken out of every error message. The package reads no environment variable and no setting for it.
- Settings in `settings/models.json`, read by the caller and passed in as options: how many retries, the first and the longest wait, how long to wait for the reply to begin, how long a stream may be silent, and the thinking budgets at which the effort asked for becomes medium and high.
- The builder is `adapters.Responses(options)`, in a file of its own, `internal/adapters/responses.go`.
- Proof: exchanges under `fixtures/responses-adapter/`, invented and written by hand from the public description of the API, replayed by an `httptest` server on this machine. Each fixture gives the seam request, the body the adapter must send, what the server answers, and the reply or error that must come out. `verify` stays green; the package's folder goes on the `offline` list.
- Not provable here: that the real API agrees with the fixtures. The live check needs a key and a network, which is the owner gate. The doc says which parts of the wire format are sure and which are inferred.
- Docs: `docs/architecture/adapter-responses.md`, a row in the overview, decision records from 0110, and the journal `2026-11-24-models-005.md`.


### Result

Provisional, on the owner gate. The adapter is built and its replayed exchanges pass. It has never spoken to the real API, because the build had no key and no network. The live check is what is left.

Built:

- `internal/adapters/responses`, a `seam.Provider` over HTTP with the standard library: the request, the stream of server-sent events, function calls, reasoning, errors sorted into the seam's kinds, and retries with a doubling wait.
- `adapters.Responses(options)` in `internal/adapters/responses.go`, the builder for `models.AdapterResponses`.
- Seven settings in `settings/models.json`, under `responses.`.
- `fixtures/responses-adapter/`: 21 invented exchanges in five files, written by hand, replayed by a server on this machine.
- The folder is on the `offline` check's list. `verify` is 13 of 13.
- `docs/architecture/adapter-responses.md`, which lists what in the wire format is sure and what is inferred, and decisions 0110, 0111 and 0112.

The proof: "Recorded-exchange tests pass" holds, with the plain statement that the exchanges were written from the public description of the API and not recorded from it. "An optional live check passes with a key" has not been run.

To clear the gate the owner supplies an OpenAI key, stored as a secret in a tenant's vault, a registry entry that names it, and a machine with a way out to the provider. The live check is then one plain answer, one turn with a tool call and its result, and one turn with reasoning on a model that reasons, where the second call sends the reasoning back. The doc's "Inferred" list is what that check should look at first.

Journal: `docs/journal/2026-11-24-models-005.md`.
