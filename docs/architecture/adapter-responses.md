# The OpenAI Responses adapter

Folder: `internal/adapters/responses`. The builder is `internal/adapters/responses.go`.

This adapter lets a registry entry whose `adapter` is `responses` be called. It is a `seam.Provider` ([seam.md](seam.md)) for one entry: it turns a `seam.Request` into one `POST` to the entry's endpoint, reads the reply as server-sent events, and turns those into the seam's events. It uses the standard library only. There is no SDK.

**It has never spoken to the real API.** The build had no key and no network. It is proven on exchanges written by hand and replayed by a server on this machine. The section "Sure and inferred" says what that proof is worth, and the item stays provisional until the live check is run.

## Using it

```go
opts, err := responses.DefaultOptions()          // reads settings/models.json
set := adapters.Set{models.AdapterResponses: adapters.Responses(opts)}
m, p, err := adapters.Open(ctx, store, set, "example-private")
reply, err := seam.Call(ctx, p, req)             // or p.Stream(ctx, req, emit)
```

`adapters.Responses(opts)` returns a `Builder`. Whoever starts the server puts it in its `Set` (decision [0052](decisions/0052-builders-are-passed-in.md)). `adapters.Open` reads the entry and the key and calls the builder, so each entry gets a provider of its own. A provider holds nothing that changes and can be used from several goroutines.

What the entry gives:

| Field | Use |
| --- | --- |
| `endpoint` | The base address. The adapter adds `/responses`, so `https://models.example/v1` is called at `https://models.example/v1/responses`. |
| `provider_model` | Sent as `model`. |
| `key` | The name of the secret. `Open` reads its value from the tenant's vault and hands it to the builder. An entry with no key sends no `Authorization` header. |
| `output_limit` | Caps `max_output_tokens`. |
| `capabilities` | `thinking` decides whether reasoning is asked for and kept. |

## The key

The key reaches the adapter as the builder's `key` argument and from nowhere else. The package reads no environment variable and no setting for it. It is put in the `Authorization: Bearer` header of each call and in nothing else: not the address, not the body.

It is kept out of everything that comes back:

- Every error message has the key replaced before it is emitted, in case a provider or a proxy repeated it.
- A refused key (401 or 403) gets the adapter's own words and the provider's code. The provider's message is dropped, because it repeats part of the key.
- A connection failure is reported without the address.
- Printing the provider, with any format verb, gives its model's name.
- `New` refuses a key with a line break in it, and says so without the key.

The tests send an invented key and look for it in every event and every error of every fixture, including two where the server repeats it.

## The request

| Seam | Responses API |
| --- | --- |
| `System` text blocks | `instructions`, joined by a blank line |
| A user message's text, images and files | One `{"role":"user","content":[…]}` item with `input_text`, `input_image` and `input_file` parts |
| An image or a file | Inline, as a `data:` address with the bytes in base64. A block that names a stored file by `Ref` is refused as `invalid`: whoever builds the request reads the file in first |
| An assistant text block | `{"role":"assistant","content":"…"}` |
| A tool call | `{"type":"function_call","call_id","name","arguments"}`. The seam's block id is the `call_id` |
| A tool result | `{"type":"function_call_output","call_id","output"}`, placed before the rest of its user message. All-text content is one string. Content with an image or a file is a list of parts |
| A failed tool result | The same, with the output starting `Tool error: `. The API has no field for it |
| A thinking block this adapter made | `{"type":"reasoning","id","summary","encrypted_content"}` |
| A thinking block from another provider | Left out |
| `Tools` | `{"type":"function","name","description","parameters","strict":false}` |
| `MaxOutputTokens` | `max_output_tokens`, no more than the entry's `output_limit`. Zero leaves it out |
| `ThinkingTokens` | `reasoning.effort` and `reasoning.summary: "auto"`, for an entry with the `thinking` capability |

Three fields are always sent: `stream: true`, `store: false`, and, for an entry with `thinking`, `include: ["reasoning.encrypted_content"]`.

**Nothing is stored at the provider.** Keystone keeps the chat, so `store` is false and no call names an earlier response. The cost is that reasoning has to travel with the chat: the provider returns it encrypted, the adapter keeps it in the thinking block's `Signature` as `responses/1:` followed by the item's id and the encrypted content, and sends it back as a reasoning item on the next call. Decision [0110](decisions/0110-responses-stores-nothing-at-the-provider.md).

**A thinking budget becomes an effort.** The API takes `low`, `medium` or `high`, not a number of tokens. A budget from `responses.effort_high_from` up asks for high, from `responses.effort_medium_from` up for medium, and anything smaller for low. A budget of zero sends no `reasoning` field, which leaves a reasoning model at its own default: the adapter cannot switch reasoning off. On these models `max_output_tokens` counts reasoning too, so a small limit can be spent before any text is written. Decision [0111](decisions/0111-a-thinking-budget-becomes-an-effort.md).

## The stream

The API sends many kinds of event. The adapter uses these and ignores the rest.

| Event | Becomes |
| --- | --- |
| `response.output_text.delta` | Text added to a text block, which is opened on the first piece |
| `response.refusal.delta` | The same, and the stop reason becomes `refused` |
| `response.content_part.done` | The text block closes |
| `response.output_item.added` for a `function_call` | A tool call block opens, with its `call_id` and name |
| `response.function_call_arguments.delta` | Input added to the tool call |
| `response.output_item.added` for a `reasoning` item | A thinking block opens |
| `response.reasoning_summary_text.delta`, `response.reasoning_text.delta` | Text added to the thinking block. A second summary part starts a new paragraph |
| `response.output_item.done` | The item's block closes. A reasoning item's encrypted content is added as the signature. Content that did not arrive in deltas is sent now, whole |
| `response.completed` | `stop`, with the usage. The reason is `tool_call` if the reply held a function call, `refused` if it held a refusal, and `end` otherwise |
| `response.incomplete` | `stop` with `max_tokens` when the reason is `max_output_tokens`, and `refused` when it is `content_filter` |
| `response.failed`, `error` | An error |

The seam's `start` event is sent with the first block or the stop, not when the provider says the response was created. A reply that fails before it says anything has then passed nothing on, and can be sent again.

**Usage.** The provider's `input_tokens` includes what it read from its cache, and the seam's does not. So `InputTokens` is `input_tokens` less `cached_tokens`, `CacheReadTokens` is `cached_tokens`, `OutputTokens` is `output_tokens`, and `ThinkingTokens` is `reasoning_tokens`. The API reports no cache writes. A reply with no usage has zero counts and `Estimated` set, so whoever records it knows to count.

**Caching.** The provider caches every prompt of 1024 tokens or more by its longest prefix, with no marks, so the adapter sends none and ignores a request's cache points. The context builder's fixed order keeps a chat's start the same from turn to turn, which is what that cache needs ([caching.md](caching.md)). `prompt_cache_key`, which keeps a chat's calls on the same machine, is not sent.

**A reply with no end is a failure.** If the connection closes before `response.completed` or `response.incomplete`, the call ends with an `unavailable` error. A reply cut by the output limit part of the way through a function call also fails, because half a call cannot be run. What arrived before a failure has been passed on, and the seam's `Collector.Partial` keeps it.

## Errors

| What happened | Kind | May be retried |
| --- | --- | --- |
| 401, 403, or a code such as `invalid_api_key` | `auth` | no |
| 429, or `rate_limit_exceeded` | `rate_limited` | yes |
| `insufficient_quota` | `rate_limited` | no |
| 503, 529, or `server_is_overloaded` | `overloaded` | yes |
| 408, 409, any other 5xx, `server_error`, no connection, a reply that breaks off or goes silent | `unavailable` | yes |
| 413, `context_length_exceeded`, or a 400 whose message speaks of the context window | `too_long` | no |
| 400, 404, 405, 422, or a code such as `unknown_parameter` or `model_not_found` | `invalid` | no |
| A request the adapter refuses before sending | `invalid` | no |
| A 200 that is not a stream of events, an event that cannot be read, anything else | `other` | no |

A code the adapter knows decides the kind, wherever it arrives: in the body of a refused call, in an `error` event, or in a failed response. Otherwise the HTTP status decides. An error's message says the status, the code and the provider's own words, cut to 300 characters.

## Retries

A call is sent again when all of these hold: the failure may be retried, nothing of the reply has been passed on, and fewer than `responses.max_retries` retries have been made. The wait starts at `responses.retry_base_delay` and doubles, up to `responses.retry_max_delay`. When the provider names a wait, in `Retry-After` or `Retry-After-Ms`, that wait is used. If it is longer than `responses.retry_max_delay` the call fails at once with the error still marked as one that may be retried, so routing can try another model. Decision [0112](decisions/0112-a-call-is-retried-only-before-its-reply-begins.md).

Once any event has been passed on, nothing is sent twice. The error's `Retryable` tells the caller whether a new call might work.

There is no random spread on the waits. One install makes few calls at once, so it was left out.

Cancelling the context stops the call at once and returns the context's error. An error from `emit` does the same with that error.

## Settings

In `settings/models.json`, read by `responses.DefaultOptions()` or `responses.OptionsFrom(registry)` and passed to the builder. The adapter reads no setting once it is built.

| Key | Default | What it is |
| --- | --- | --- |
| `responses.max_retries` | 2 | Retries after the first call |
| `responses.retry_base_delay` | 500ms | The wait before the first retry |
| `responses.retry_max_delay` | 8s | The longest wait |
| `responses.header_timeout` | 60s | How long to wait for the reply to begin |
| `responses.idle_timeout` | 120s | How long a stream may send nothing |
| `responses.effort_medium_from` | 4096 | The thinking budget from which effort is medium |
| `responses.effort_high_from` | 16384 | The thinking budget from which effort is high |

There is no limit on the length of a whole reply. A long answer that keeps arriving is not cut.

Two values are fixed in code because they guard against a broken endpoint and are not for tuning: one event may hold 32 MB, and 64 KB of a refused call's body is read.

## Sure and inferred

Nothing below was checked against the real API in this build. "Sure" means it is a long-standing, central part of the published API that the author knows well. "Inferred" means it is believed and less certain, or it is a guess about behaviour the documents leave open.

**Sure**

- `POST {base}/responses` with `Authorization: Bearer`, a JSON body, and `stream: true` giving server-sent events whose `data` is JSON with a `type`.
- `model`, `instructions`, `input` as a list of items, `max_output_tokens`, `store`, `tools` with function tools that are flat (`name` beside `type`, not nested).
- User content parts `input_text` and `input_image` with `image_url`; `function_call` and `function_call_output` items tied by `call_id`; an assistant message with a string for content.
- The events `response.output_text.delta`, `response.output_item.added`, `response.output_item.done`, `response.function_call_arguments.delta` and `response.completed`, and their fields `delta`, `item_id`, `item` and `response`.
- Usage: `input_tokens`, `output_tokens`, `input_tokens_details.cached_tokens`, `output_tokens_details.reasoning_tokens`, with cached tokens counted inside `input_tokens`.
- The error body `{"error":{"message","type","param","code"}}`, and 401, 429 and 5xx meaning what the table says.
- `reasoning.effort` with `low`, `medium` and `high`.

**Inferred**

- `input_file` with `filename` and `file_data` as a `data:` address. Which file types a model reads is the provider's choice.
- A `function_call_output` whose `output` is a list of parts, used only when a tool returns an image or a file.
- `strict: false` on each tool. It is sent because the API is believed to treat a missing `strict` as true, which refuses many ordinary schemas.
- `include: ["reasoning.encrypted_content"]` and a reasoning item sent back with its `id`, `summary` and `encrypted_content` while `store` is false. The live check must show that a second call of a turn with such an item is accepted.
- `reasoning.summary: "auto"`, the events `response.reasoning_summary_part.added` and `response.reasoning_summary_text.delta`, and `response.reasoning_text.delta`.
- `response.refusal.delta`, and `response.incomplete` with `incomplete_details.reason` of `max_output_tokens` or `content_filter`.
- The shape of the `error` event (`code` and `message` at the top) and of `response.failed` (`response.error`). The adapter reads both shapes for the first.
- The list of error codes. Only `invalid_api_key`, `rate_limit_exceeded`, `insufficient_quota`, `context_length_exceeded` and `server_error` are well known. The others are there in case.
- The `Retry-After-Ms` header.
- That sending no `reasoning` field to a reasoning model leaves it at its default, and that a model without reasoning refuses the field. The adapter sends it only for an entry with the `thinking` capability.
- That output items arrive one after another and never interleaved. The adapter closes the open block when a different one starts, so interleaved items would come out as more blocks, not as a failure.

Other servers that speak this API (a gateway, a local model server) may differ in any of these. The adapter is lenient where that is free: it takes an event's type from the `event:` line when the data has none, ignores `[DONE]` and comments, and accepts an item that arrives whole.

Since P9-02 the adapter refuses, while it builds the body and before anything is sent, a request that holds an image for an entry without `vision`, with the seam's `invalid`. `vision_test.go` proves it against a stand-in, and that an entry with `vision` sends the image as an `input_image` data address (decision [0383](decisions/0383-the-adapters-refuse-an-image-to-a-model-without-vision.md)).

## What proves it

`go test ./internal/adapters/responses`. The exchanges are in `fixtures/responses-adapter/`, 21 cases in five files, all invented and written by hand. An `httptest` server on this machine replays them. For each case the test checks the path, the header and the body the adapter sends, how many calls it makes, the whole reply through the seam's `Collector` or the error's kind and whether it may be retried, and that the key is in no event and no error.

Other tests: cancelling stops the stream, a silent stream is given up on, an error from `emit` ends the call, an endpoint that is not there fails without its address in the message, a request the adapter refuses never reaches the endpoint, `New` refuses a bad entry without showing the key, the options read from the settings, every status is sorted, and the event reader handles comments, several `data` lines and a cut-off event.

The folder is on the `offline` check's list of folders that may import a network package ([tools.md](tools.md)). Its tests call this machine only, and pass with no key and no way out to the network.

The `no keys in output` check has no line for this adapter, because `tools/verify` may not import a network package and so cannot run a replay server. The adapter's own tests do that looking.

## Not built

- The live check against the real API. It needs a key and a network.
- The provider's own tools (web search, file search, code interpreter), stored responses and `previous_response_id`, background responses, structured output, and audio. Their events are ignored.
- Citations on output text. The text arrives without them.
- A certificate of its own for an endpoint. The entry has no field for one yet.
- Uploading a file to the provider. Files go inline.
- A shared reader for server-sent events. This adapter has its own, as each adapter does for now.
