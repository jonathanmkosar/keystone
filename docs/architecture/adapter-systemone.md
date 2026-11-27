# Systemone adapter

`internal/adapters/systemone` (P14-02) speaks Ollama's decision API, `POST {endpoint}/v1/systemone`, which Ollama serves from version 0.35.1 for decision models. It is a `seam.Decider` built from one registry entry, with the standard library only. The seam's side of a decision call is in [seam.md](seam.md); who calls it and how each call is recorded is in [decision-calls.md](decision-calls.md).

An entry of this adapter is a decision model: it lists the capability `decision` and nothing else, and the registry refuses a `systemone` entry without it ([models.md](models.md)). It takes decision calls only, so its `Stream` refuses every request with one error event of the kind `invalid`.

## An entry

    {"name": "house-decide", "label": "House decisions", "adapter": "systemone",
     "endpoint": "http://192.168.1.20:11434", "provider_model": "example-decide:9b",
     "context_window": 8192, "output_limit": 1, "capabilities": ["decision"],
     "price": {"input": 0, "output": 0}, "class": "local"}

`endpoint` is Ollama's own address, without `/v1`: the adapter adds `/v1/systemone`. An endpoint written with `/v1` on it, as a Chat Completions entry for the same Ollama is, is not given a second. A local Ollama asks for no key, so `key` is usually left out; an endpoint that asks for one names it, and it is sent as a bearer token. `ca_cert` works as for the Chat Completions adapter (a copy of its reader is kept here so the two stay apart). A redirect is never followed, and a local entry never goes through a proxy.

## The translation

A `seam.DecisionRequest` becomes:

    {"model": "<provider_model>", "state": "<state>",
     "questions": {
       "step":   {"type": "choice", "instructions": "...", "criteria": {"house-chat": "House chat: step 1 of 3", "mid-hosted": null}},
       "simple": {"type": "noul", "instructions": "...", "criteria": {"true": "It is", "false": "It is not"}}}}

- Questions are an object keyed by id, and a choice's options an object keyed by id with the description as the value, `null` when there is none. Both are written in the request's order (an ordered writer, `object`), so the model reads options as the caller listed them.
- A `yes_no` question is Ollama's `noul`. Its `criteria` holds only the answers the question describes, `true` for yes and `false` for no; with none, Ollama's defaults stand.
- Ollama's `score` questions and `images` are not used.

The answer:

    {"model": "...", "answers": {
       "step":   {"type": "choice", "choice": "house-chat", "probabilities": {"house-chat": 0.91, "mid-hosted": 0.09}, "confidence": 0.56},
       "simple": {"type": "noul", "noul": 0.8}},
     "usage": {"input_tokens": 212, "output_tokens": 2}}

becomes one `seam.DecisionAnswer` for each question, in the request's order. A choice keeps its probabilities; a `noul` of p gives yes p and no 1 - p. The choice is worked out from the probabilities (`seam.ChoiceOf`), so it is always the most probable option, the earlier on a tie. Ollama's `confidence` is not kept: it measures how spread the probabilities are, which the caller can work out itself. `usage` is kept as given; Ollama's output tokens are its internal scoring tokens. The result's model is the request's registry name. An answer that does not answer the request (a question missing, an option missing or not asked, a probability outside 0 to 1, a set that does not sum to one within a hundredth, or a choice answered as a yes or no) is refused as `other` (`Read`, with `seam.ValidateDecisionResult`).

## Limits, before anything is sent

- More questions than `systemone.max_questions`: refused, `too_long`, nothing sent.
- A request body, as `Body` writes it, larger than `systemone.max_request_bytes`: refused, `too_long`, nothing sent. Exactly at the limit is sent. The body is the state, the questions and the options: what Ollama counts as its 64 KiB of text and schema.
- A request the seam refuses (no state, a choice of one option, an id with a space): refused, `invalid`, nothing sent.

These are the proof's second line, in `TestLimitsAreKeptBeforeSending`.

## One attempt, within the timeout

A call is sent once and must be answered within `systemone.timeout`, from connecting to the whole answer. A late call fails as `unavailable` and retryable, saying how long it waited; the router does not wait for it and lets the rules decide (decision [0632](decisions/0632-the-systemone-adapter-sends-once-and-keeps-ollamas-limits-before-sending.md)). A call whose own context is done returns the context's error.

## Errors

Ollama's error body is `{"error": "..."}`. Its message is kept, on one line and cut to 300 characters, with the key taken out.

| Status | Kind |
| --- | --- |
| 400 that mentions the context | `too_long` |
| 400 | `invalid` |
| 401, 403 | `auth` |
| 404 (the model is not pulled) | `invalid` |
| 413 | `too_long` |
| 429 | `rate_limited`, retryable |
| 503 | `overloaded`, retryable |
| other 5xx | `unavailable`, retryable |
| anything else | `other` |

A connection that fails is `unavailable` and retryable; an untrusted certificate is `unavailable` and says to name the authority in `ca_cert`.

## Settings

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `systemone.timeout` | 5s | install | The longest one decision call may take. |
| `systemone.max_request_bytes` | 65536 | install | The largest request body sent, in bytes. |
| `systemone.max_questions` | 64 | install | The most questions in one call. |

`adapters.SystemoneOptions` reads them from the registry's defaults, as the other adapters' options are read, and `adapters.Real` puts the adapter in the server's set.

## In code

- `systemone.go`: `New`, `Options`, `Decide`, `Stream`, the error mapping.
- `wire.go`: `Body` and `Read`, the translation.
- `internal/adapters/decide.go`: `OpenDecider`, `Decides`, `Systemone`, `SystemoneOptions`.

## Checked by

- `go test ./internal/adapters/systemone`: the recorded exchanges in `fixtures/systemone/exchanges.json` (the router's two questions on a local Ollama with no key, two yes or no questions on an endpoint with a key and `/v1` in its address), each refusal's kind and that it is sent once and holds no key, each malformed answer refused, the limits kept before sending, the timeout, a cancelled call, a redirect not followed, `Stream` refused.
- The `sensitive content` check in `verify`, which opens three decision models on this adapter's name with the fake behind them ([decision-calls.md](decision-calls.md)).

## Not here yet

- **The live check.** No real Ollama has been called. It waits on the owner's Ollama 0.35.1 or later with the decision model pulled (P14-02's owner gate). The wire shape is from Ollama's published API page, and the exchanges are hand-written from it.
- Images and `score` questions, which nothing asks for.
