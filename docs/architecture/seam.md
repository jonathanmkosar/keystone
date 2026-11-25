# Model seam: the internal format

Folder: `internal/seam`

Keystone's own shape for a model call and its streamed reply. Everything outside the adapters speaks this format. Each adapter translates it to and from one provider's format, so no provider's format leaks into chats, context, tools or the interface.

Today the folder holds the format and the one interface every provider implements. The fake provider has its own doc, [fake-provider.md](fake-provider.md). The registry, the three adapters, routing, the budget and usage records arrive with P3-02 to P3-08.

## Content blocks

A `Block` is one piece of content. Its `type` says which fields it uses. A field that does not belong to the type must be empty.

| Type | Fields | Notes |
| --- | --- | --- |
| `text` | `text` | Never empty. |
| `image` | `media_type`, and `data` or `ref` | The media type starts with `image/`. |
| `file` | `media_type`, `name`, and `data` or `ref` | |
| `tool_call` | `id`, `name`, `input` | `input` is a JSON object. |
| `tool_result` | `id`, `content`, `is_error` | `id` is the tool call's id. `content` holds text, image and file blocks. |
| `thinking` | `text`, `signature` | The signature is the provider's own proof, and goes back unchanged. A withheld thinking block has a signature and no text. |

`data` is the bytes, sent inline. `ref` names a file in the tenant's `files/` folder by its hash. A block has one or the other. Whoever builds the call for a provider reads the file behind a `ref`.

## Messages and requests

A `Message` is a `role`, `user` or `assistant`, and its blocks. Tool calls and thinking belong to the assistant. Tool results come back in a user message.

A `Request` is one model call:

- `model`: the registry name. Empty means the routing rules choose.
- `system`: the standing instructions, as text blocks. There is no system role.
- `messages`, and the `tools` on offer. Each tool has a name, a description and a JSON Schema for its input.
- `max_output_tokens` and `thinking_tokens`. Zero means the model's own limit, and no thinking.

`ValidateRequest` also checks the tool calls across messages: every tool result answers a call in an earlier message, no call is answered twice, and no call id is used twice.

## The stream

A reply arrives as `Event` values. Like a block, an event has a `type` that says which fields it uses.

| Type | Fields | Meaning |
| --- | --- | --- |
| `start` | `model` | The stream opens. |
| `block_start` | `index`, `block` | A text, thinking or tool call block opens. A tool call carries its id and name. |
| `block_delta` | `index`, and `text`, `input` or `signature` | More of the open block. `input` is a piece of JSON text, whole only when the pieces are joined. |
| `block_stop` | `index` | The block is complete. |
| `stop` | `stop_reason`, `usage` | The reply is complete. |
| `error` | `error` | The call failed. Nothing follows. |

Blocks open and close one at a time, in order of `index` from zero.

Stop reasons: `end`, `tool_call`, `max_tokens`, `refused`.

An `Error` has a `kind`, a `message` and `retryable`. Kinds: `auth`, `rate_limited`, `overloaded`, `too_long`, `invalid`, `unavailable`, `other`. Routing reads the kind to decide whether to retry or fall back. An adapter must never put a key in the message.

## Usage

`Usage` is the token count of one call, carried by the `stop` event.

- `input_tokens`: input that was neither read from nor written to the provider's cache.
- `output_tokens`: everything the model wrote, thinking included.
- `cache_read_tokens` and `cache_write_tokens`.
- `thinking_tokens`: the part of the output spent on thinking.
- `estimated`: true when Keystone counted because the provider did not report.

The first four do not overlap, so `Total` is their sum. Providers count cached input in different ways, and each adapter converts to this one. Cost is not here: it is worked out from the registry's prices when the call is recorded.

## Collecting a stream

- A `Collector` takes events one at a time and builds a `Response`: the model, the assistant message, the stop reason and the usage. It refuses an event that is malformed or out of order. An `error` event comes back from `Add` as the `*Error` itself.
- `Partial` gives what has arrived so far, for a turn that is cancelled or fails. An open tool call is left out, because half its input is no use.
- `Collect` does the same for a whole list of events.
- `Events` goes the other way: it turns a `Response` into the shortest stream that collects back to it. The fake provider builds on it.

A block that opens and closes with nothing in it is dropped. A tool call that receives no input gets `{}`.

## Providers

A `Provider` is anything that answers a `Request` with a stream: each of the three adapters, and the fake. It has one method.

```go
Stream(ctx context.Context, req Request, emit func(Event) error) error
```

`Stream` calls `emit` once for each event, in order, and returns when the stream is over. It never calls `emit` from another goroutine.

- A call that works ends with a `stop` event, and `Stream` returns nil.
- A call that fails ends with an `error` event, and `Stream` returns that `*Error`. A reply that breaks off before its `stop` event is a failure too. The fake reports it as `unavailable` and retryable, and the adapters should do the same. So nil always means the reply is whole.
- When `emit` returns an error, `Stream` sends nothing more and returns that error. This is how a receiver that has gone away ends the call.
- When the context is cancelled, `Stream` sends nothing more and returns the context's error. This is how a turn is stopped.

A collector's `Add` is an `emit`, so a caller can pass it as it is, or wrap it to send each event on to a browser first.

`Call` is for a caller that wants the whole reply and not the stream, such as a title or a summary. It runs a provider through a collector and returns the `Response`, or the call's `*Error`.

A provider is safe to use from several goroutines at once. Decision [0015](decisions/0015-a-provider-streams-through-a-callback.md) says why `emit` is a function and not a channel.

## JSON

Every type has JSON tags, and that JSON is how the format is stored and sent: in the chat record, the audit log, the API's event stream and the fake provider's fixtures. The `Encode` functions validate before writing. The `Decode` functions refuse an unknown field and validate after reading.

## Checked by

`go test ./internal/seam`. Every block type and every event type goes through JSON and comes back the same. Every block type a model can write goes through a stream and comes back the same. Malformed blocks, messages, requests, events and out-of-order streams are each refused. `Call` returns a whole reply, the stream's own error, an error for a stream with no stop event, and the context's error when the call is cancelled.

## Not here yet

- What a call needs (vision, privacy, a cost ceiling) is part of routing, P3-08.
- Cache markers on blocks come with caching, P5-03, and the Anthropic adapter.
- Embedding calls get their own small request shape in P6-08.
