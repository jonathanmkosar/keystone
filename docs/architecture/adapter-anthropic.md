# Anthropic Messages adapter

Folder: `internal/adapters/anthropic`

The adapter for the Anthropic Messages API. It is a `seam.Provider`: it turns a seam request into one streamed HTTP call, and turns the reply's server-sent events back into seam events. Nothing outside this folder sees the provider's format.

It uses Go's standard library and no SDK. The base address comes from the registry entry, so the same adapter serves Anthropic and any gateway that speaks the same API, such as Ollama and LiteLLM.

**Not yet proven against the real API.** No key was to hand when it was built. It is proven on exchanges written by hand from the published format and replayed by a server on this machine. The live check at the end of this page is the owner's to run.

## The API version

Every call carries `anthropic-version: 2023-06-01`. The value is the setting `anthropic.version`. That version was chosen because the gateways speak it. The adapter sends no sampling parameters (`temperature`, `top_p`, `top_k`), which are what the later `2026-10-21` version removes, so moving to it should be a change to the setting. That has not been tried.

## Making a provider

```go
set := adapters.Set{}
err := adapters.AddAnthropic(set, registry)            // reads the settings, adds the builder
m, p, err := adapters.Open(ctx, store, set, "example-large")
reply, err := seam.Call(ctx, p, seam.Request{Model: m.Name, Messages: messages})
```

- `adapters.AddAnthropic(set, registry)` puts the builder into a set under `models.AdapterAnthropic`. It is in `internal/adapters/anthropic.go`, a file of its own.
- `adapters.AnthropicOptions(registry)` reads the settings, and `adapters.Anthropic(options)` returns the builder, for a caller that wants to change an option first.
- `anthropic.New(entry, key, options)` makes one provider directly.

Every provider a builder makes shares one HTTP client, so connections are kept between calls even though `Open` makes a new provider each time.

The adapter reads no setting and no environment variable. The settings are read in `internal/adapters` and passed in as `anthropic.Options`.

### What the entry gives

| Field | Use |
| --- | --- |
| `endpoint` | The base address. The adapter calls `{endpoint}/v1/messages`. An address that already ends in `/v1` is not given a second one. |
| `provider_model` | Sent as `model`. |
| `output_limit` | Sent as `max_tokens` when the request gives no limit, and the most that is ever sent. |
| `capabilities` | Thinking is asked for only when the entry lists `thinking`. |
| `name` | The model name in the `start` event. A request that names another model is refused. |

The builder refuses an entry for another adapter, one with no `provider_model` or output limit, and an endpoint that is not a plain `http` or `https` address.

## The key

The key is the builder's `key` argument, which `adapters.Open` reads from the tenant's vault. It goes out in the `x-api-key` header and nowhere else. With no key, as for a local gateway, the header is left out.

- The provider holds the key as a function, so printing a provider with any verb cannot print it.
- Every error message passes through one function that takes the key out, in case an endpoint repeats it.
- An error for an endpoint that could not be reached does not repeat the address.
- A redirect is not followed. Go would send the header on to wherever it points.
- A key with a line break or a control character in it is refused, without being repeated.
- The package writes no log.

`verify`'s `no keys in output` check has a surface for the adapter, `tools/verify/anthropic.go`. It calls `anthropic.KeyOutputs`, which runs a provider against stand-in endpoints that fail in each way and repeat the key in their answers, and returns every error, every event and the provider as printed.

## What is sent

| Seam | Messages API |
| --- | --- |
| `system` text blocks | `system`, a list of text blocks |
| `text` | `text` |
| `image` with `data` | `image` with a base64 source |
| `file`, `application/pdf` | `document` with a base64 source, the name as its title |
| `file`, any `text/` type or `application/json` | `document` with a plain text source. It must be UTF-8. |
| `file` whose type starts with `image/` | `image` |
| `tool_call` | `tool_use` |
| `tool_result` | `tool_result`, with `tool_use_id`, its content and `is_error` |
| `thinking` with a signature | `thinking`, with its text and signature unchanged |
| `thinking` whose signature starts with `redacted:` | `redacted_thinking`, with the rest as its `data` |
| `thinking` with no signature | Left out. It came from another provider, and this API refuses it. |
| `tools` | `tools`: `name`, `description`, `input_schema` |
| `max_output_tokens` | `max_tokens`, never more than the entry's output limit |

Refused before any call, as an `invalid` error:

- A request the seam itself refuses.
- An image or a file given as a `ref`. The adapter cannot reach a tenant's files. Whoever builds the call reads the file and sends its bytes.
- A file of any other type.

### Thinking

When the request has `thinking_tokens` above zero and the entry lists the `thinking` capability:

- A model whose `provider_model` is on the list in `anthropic.budget_thinking_models` is sent `{"type": "enabled", "budget_tokens": n}`. The budget is the request's, kept under `max_tokens`. Under 1024, the least the API takes, no thinking is asked for.
- Every other model is sent `{"type": "adaptive", "display": "summarized"}`. The model decides how much to think, and the number in the request is not sent. The summary is asked for because without it the thinking arrives as empty blocks.

With `thinking_tokens` at zero nothing is sent about thinking. Some models think anyway, and their thinking blocks are passed on like any other.

Decision [0101](decisions/0101-thinking-form-by-model-name.md) says why the form is chosen by the model's name.

### Cache control

The provider reads tools, then system, then messages, and caches everything up to a block marked `{"type": "ephemeral"}`, at most four marks in a request. With `anthropic.cache_control` on, the adapter marks where the request's cache points say (P5-03, decision [0331](decisions/0331-the-seam-carries-cache-points-and-the-anthropic-adapter-makes-four-marks.md)), up to four places:

1. The end of the standing start: system block `cache.start`, counted from one. With an empty start, the last tool.
2. The last system block, when there are blocks after the standing start.
3. The end of the history: the last block of message `cache.history`, counted from one, among the messages sent.
4. The last block of the last message, which the next step of a turn of tool calls reads.

A thinking block cannot carry a mark, so a mark that would fall on one goes on the block before it. A message the adapter leaves out is allowed for when the history is counted. [caching.md](caching.md) says how the context builder sets the points and what each mark saves.

A request with no cache points, such as a title or a summary, gets the two marks of decision [0100](decisions/0100-the-anthropic-adapter-places-cache-marks.md): the last system block (or the last tool when there is none) and the last block of the last message that is not thinking. A gateway that does not cache ignores the marks, and the setting turns them all off for one that refuses them.

## What comes back

| Messages API | Seam |
| --- | --- |
| `message_start` | `start`, with the entry's name. The first token counts. |
| `content_block_start`: `text`, `thinking`, `tool_use` | `block_start` |
| `content_block_start`: `redacted_thinking` | `block_start` for a thinking block, and a `block_delta` whose signature is `redacted:` and the block's data |
| `text_delta`, `thinking_delta`, `signature_delta`, `input_json_delta` | `block_delta` with `text`, `text`, `signature` or `input` |
| `content_block_stop` | `block_stop` |
| `message_delta` | The stop reason and the later token counts, kept for the end |
| `message_stop` | `stop`, with the stop reason and the usage |
| `ping`, and any event the adapter does not know | Nothing |
| `error` | `error` |

The provider numbers every block. The seam numbers only the kinds it has, so a block of another kind, such as one of the provider's own tools, is read past and the blocks after it are renumbered. An empty delta is dropped, since the seam refuses one.

Events are read by their `type` field, so a gateway that sends `data:` lines with no `event:` line works too.

| `stop_reason` | Seam |
| --- | --- |
| `end_turn`, `stop_sequence`, `pause_turn`, anything new | `end` |
| `tool_use` | `tool_call`, or `end` if the reply holds no tool call |
| `max_tokens`, `model_context_window_exceeded` | `max_tokens` |
| `refusal` | `refused` |

### Usage

| Messages API | Seam |
| --- | --- |
| `input_tokens` | `input_tokens` |
| `output_tokens` | `output_tokens` |
| `cache_read_input_tokens` | `cache_read_tokens` |
| `cache_creation_input_tokens` | `cache_write_tokens` |

The API's input count already leaves out what was read from and written to the cache, which is how the seam counts, so nothing is subtracted. The API does not say how much of the output was thinking, so `thinking_tokens` is zero. A gateway that reports no counts gives zeros, and the adapter does not estimate.

## Errors

A failure becomes a `seam.Error`. The provider's own error type is trusted first. When there is none, as with a gateway's error page, the HTTP status decides.

| Error type, or status | Kind | Retryable |
| --- | --- | --- |
| `authentication_error`, `permission_error`, 401, 403 | `auth` | no |
| `rate_limit_error`, 429 | `rate_limited` | yes |
| `overloaded_error`, 529 | `overloaded` | yes |
| `request_too_large`, 413 | `too_long` | no |
| `invalid_request_error`, `not_found_error`, 400, 404, 422, when the message is about the size of the prompt | `too_long` | no |
| the same, otherwise | `invalid` | no |
| `api_error`, `timeout_error`, 408, 500 and above | `unavailable` | yes |
| An endpoint that could not be reached, a reply that broke off, a stream that went quiet | `unavailable` | yes |
| A redirect | `unavailable` | no |
| `billing_error`, 402 | `other` | no |
| 409 | `other` | yes |
| A stream that is not JSON or is out of order, anything else | `other` | no |

The API has no error type for a prompt that does not fit, so the adapter reads the message for it. That list of phrases is in `errors.go`, and is the part of the table most likely to need a change after the live check.

An `x-should-retry` header of `true` or `false` overrides the last column. The message gives the status, the provider's type, its message and the `request-id` header, cut to 400 characters.

As the seam asks: a call that fails sends an `error` event and returns that error, a cancelled call returns the context's error and sends nothing more, and a receiver whose `emit` fails gets its own error back.

## Retries

A call is sent again only when all of these hold:

- the failure is retryable;
- no event has been passed on yet, so the receiver cannot see part of a reply twice;
- fewer than `anthropic.max_retries` retries have been made;
- the provider did not ask, with `retry-after` or `retry-after-ms`, for a wait longer than `anthropic.retry_max_delay`. When it does, the call fails at once, so routing can try another model.

The first wait is `anthropic.retry_base_delay`, and each later one is twice the one before, up to `anthropic.retry_max_delay`. Each wait is cut at random to between half and all of itself, so calls that failed together do not come back together. A wait the provider asked for is honoured when it is the longer.

Routing (P3-08) decides about falling back to another model. These retries are for the same model at the same endpoint.

## Token counting

`Provider.CountTokens(ctx, request)` posts the same system blocks, messages, tools and thinking to `{endpoint}/v1/messages/count_tokens` and returns the endpoint's `input_tokens`. It is a method beside `Stream`, and not part of `seam.Provider`. A caller reaches it with a type assertion:

```go
if counter, ok := p.(interface {
    CountTokens(context.Context, seam.Request) (int, error)
}); ok { ... }
```

An endpoint with no such call answers with an error, usually `invalid` for a 404, and the caller estimates. The budget (P3-07) decides when a count is worth a call.

## Settings

In `settings/models.json`. All are for the install.

| Key | Default | Meaning |
| --- | --- | --- |
| `anthropic.version` | `2023-06-01` | The `anthropic-version` header. |
| `anthropic.max_retries` | `2` | How many times a call is sent again. |
| `anthropic.retry_base_delay` | `500ms` | The wait before the first retry. |
| `anthropic.retry_max_delay` | `20s` | The longest wait. |
| `anthropic.header_timeout` | `5m` | How long the endpoint may take to begin its answer. |
| `anthropic.idle_timeout` | `5m` | How long a stream may send nothing. |
| `anthropic.cache_control` | `true` | Whether the cache marks are sent, those the cache points ask for and the two of decision 0100 alike. |
| `anthropic.budget_thinking_models` | a list of older model names | The models asked for thinking as a token budget. |

They are read at their defaults. An install's own stored values are not built yet (see [settings.md](settings.md)).

Since P9-02 the adapter refuses, while it builds the body and before anything is sent, a request that holds an image for an entry without `vision`, with the seam's `invalid`; `CountTokens` refuses it too. `vision_test.go` proves it against a stand-in, and that an entry with `vision` sends the image as a base64 `image` block (decision [0383](decisions/0383-the-adapters-refuse-an-image-to-a-model-without-vision.md)).

## Checked by

`go test ./internal/adapters/anthropic ./internal/adapters`, with no key and no network.

- Every exchange under `fixtures/anthropic/` is replayed by a server on this machine: 40 of them, five of them the cache points' marks (`cache.json`, P5-03). For each, the body the adapter sends is compared with the fixture's, the headers are checked, every event is checked against the seam's own rules, and the reply or the error is compared with the fixture's. [The fixtures' README](../../fixtures/anthropic/README.md) lists what each file holds.
- A cancelled call returns the context's error and sends nothing more. A receiver that goes away ends the call. A stream that goes quiet is given up, and the text that had arrived is kept.
- An endpoint that cannot be reached is tried again the set number of times, and its error repeats neither the key nor the address.
- The waits double up to the longest, and a call is not sent again after part of its reply was passed on.
- A token count sends the request with no output limit, and an endpoint without the call gives an error.
- Requests of every shape with every cache point get never more than four marks, none on a thinking block, the end of the standing start and of the last message always marked, and none with the setting off (`cache_test.go`).
- The event reader handles comments, both kinds of line ending, data on several lines, and an event cut off at the end.
- Entries and options that are not valid are refused, and no refusal holds the key.
- The key is in nothing the adapter gives back.
- The options come from the settings registry, and an entry that names the adapter opens once the builder is in the set.

In `verify`: the folder is on the `offline` check's list of folders that may use the network, because it imports `net/http`, and the `no keys in output` check has the surface described above ([tools.md](tools.md)).

## The live check

This is the owner gate. It needs one thing: an Anthropic API key, stored in a tenant's vault. Nothing is put in the repo, in a setting or in the environment.

1. Store the key, typed at the prompt: `go run ./tools/tenant secret set <tenant id> provider.anthropic`.
2. Add a registry entry for that tenant with `"adapter": "anthropic"`, `"endpoint": "https://api.anthropic.com"`, `"key": "provider.anthropic"`, a current model as `provider_model`, and `"class": "hosted"`. There is no command or screen for this yet (see [models.md](models.md)), so today it is a few lines of Go, or the registry route once P2-05 has one.
3. Open it with `adapters.Open` and make four calls: a plain question, a question with a tool, a question with `thinking_tokens` set, and the same long system text twice to see `cache_read_tokens` above zero on the second. Then `CountTokens`.

Since P5-03 the cache can also be checked in a real chat: send a second message within five minutes of the first, with standing instructions long enough to cache (the model's least, 1024 tokens or more), and open the inspector on the second reply. Its usage line shows the tokens read from the cache ([caching.md](caching.md)).

What the check should settle: that the format in the fixtures is the format on the wire, that `display` is accepted with adaptive thinking on the models in use, that the phrases read for a prompt that is too long are the API's, and whether the default list in `anthropic.budget_thinking_models` is right for the models in the registry.

A check against Ollama or LiteLLM needs only a registry entry with the gateway's address, and no key when it asks for none.

## Not here yet

- The live check above.
- A cache marker on the seam's own blocks, P5-03. Built since as cache points on the request, which the adapter turns into its marks; a request with none still gets the adapter's two.
- A longer cache lifetime. The marks ask for the provider's default.
- Files read from a `ref`. The turn (P4-03) reads the bytes first.
- The provider's own tools, such as its web search. The adapter sends only Keystone's tools, and reads past blocks from the provider's.
- Eager streaming of tool input. A tool call's input arrives once the provider has checked it.
- An endpoint that ignores `stream` and answers with one JSON message. It is reported as a reply that broke off.
- A sign-in other than `x-api-key`, such as a bearer token for a gateway that wants one.
- The effort setting, the refusal fallbacks and the other newer parts of the API. `thinking_tokens` is the seam's one dial.
- A count of thinking tokens. The API does not report one.
