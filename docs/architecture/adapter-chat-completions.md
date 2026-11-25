# Chat Completions adapter

Folder: `internal/adapters/chatcompletions`. Its builder: `internal/adapters/chatcompletions.go`.

The adapter for every endpoint that speaks the OpenAI-style Chat Completions format: OpenRouter, LiteLLM, Ollama, the Mac mini gateway, and anything else that copies it. It is a `seam.Provider` built from one registry entry. It uses the standard library only.

It has been proven on recorded exchanges replayed from servers on this machine. It has not yet spoken to a real endpoint: that is the owner gate, at the end of this page.

## An entry

```json
{
  "name": "study-gateway",
  "label": "Study gateway",
  "adapter": "chat_completions",
  "endpoint": "https://gateway.home.arpa:8443/v1",
  "provider_model": "example-medium",
  "key": "provider.gateway",
  "ca_cert": "/etc/keystone/gateway-authority.pem",
  "context_window": 64000,
  "output_limit": 8000,
  "capabilities": ["tools", "vision"],
  "price": {"input": 0, "output": 0},
  "class": "local"
}
```

- **`endpoint`** is the base address, up to and including the version: `http://127.0.0.1:11434/v1` for Ollama, `https://openrouter.ai/api/v1` for OpenRouter, a LiteLLM proxy's `/v1`. The adapter adds `/chat/completions`.
- **`key`** names the secret in the tenant's vault. Leave it out for an endpoint that asks for none, and no `Authorization` header is sent. With one, the header is `Authorization: Bearer` and the key.
- **`ca_cert`** is new with this adapter. It names one certificate authority to trust for this entry: PEM text, or the path of a PEM file on the machine Keystone runs on. It is for an endpoint with its own certificate, such as the gateway. See below.

The other fields are in [models.md](models.md).

## The certificate

An entry with `ca_cert` trusts that authority and no other for its own calls. An entry without one trusts the usual authorities. Other entries are not affected.

- The endpoint's certificate is still checked in full: its signature, its dates, and that it is for the host in `endpoint`.
- Nothing turns the check off. There is no setting and no field for it, and a test fails if the adapter's code ever names the switch.
- The registry refuses `ca_cert` on an `http` endpoint, on the `fake` adapter, and when it holds a private key. The adapter refuses a value that holds no certificate, or a PEM block that is not one.
- TLS 1.2 is the oldest version accepted.

Decision [0120](decisions/0120-an-entry-trusts-one-authority.md) says why it is a field of the entry and why the usual authorities are left out.

## The key

The key reaches the adapter as the `key` argument of its builder, from `adapters.Open`, which read it from the tenant's vault. The adapter reads no environment variable for it. It is held in memory for as long as the provider is, sent in the one header, and taken out of every message the adapter writes, including a reason the endpoint sent back with the key repeated in it.

## What is sent

One `POST` to `endpoint` + `/chat/completions`, with `stream` set and `stream_options.include_usage` set so the endpoint reports tokens.

| Seam | Chat Completions |
| --- | --- |
| `system` blocks | One `system` message, the blocks joined by a blank line. |
| A user message of text | A `user` message whose content is one string. |
| A user message with an image or a file | A `user` message whose content is a list of parts. An image is an `image_url` part holding a data URL. A file of text or JSON is a text part that starts with its name. Any other file is a `file` part holding a data URL. |
| A `tool_result` block | A `tool` message of its own with the call's id, placed before the rest of the user message. A failed tool's text starts with "The tool failed." An image or a file a tool returned follows in the user message, with a line in the tool message saying so. |
| An assistant message | An `assistant` message: its text as one string, its tool calls in `tool_calls` with their input as JSON text. |
| A `thinking` block | Left out. The format has no place for it, and a signature belongs to the provider that made it. |
| `tools` | `tools`, each a `function` with its name, description and schema. |
| `max_output_tokens` | `max_tokens`, and never more than the entry's `output_limit`. Left out when zero. |
| `thinking_tokens` | Not sent. See "Not here yet". |
| `model` | The entry's `provider_model`. |

An image or a file must arrive with its bytes. A block that only names a stored file by `ref` is refused as `invalid` before any call, because whoever builds the call reads the file ([seam.md](seam.md)).

## What comes back

The reply is read as server-sent events. Comment lines, which OpenRouter sends while it waits, are skipped. `data: [DONE]` ends it.

- **Text** in `delta.content` is sent on as it arrives, as a text block.
- **Reasoning** in `delta.reasoning_content` (LiteLLM and others) or `delta.reasoning` (OpenRouter, Ollama) is sent on as it arrives, as a thinking block with no signature.
- **Tool calls** are kept until the reply ends and then sent, one whole block after another, after the text. Decision [0121](decisions/0121-tool-calls-are-held-until-the-reply-ends.md) says why. Pieces of one call share an `index`. A new id at an index already seen is a new call, because some endpoints send every call whole at index 0. A call with no id is given one. Arguments are read whether they come as JSON text or as an object.
- **A refusal** in `delta.refusal` is sent on as text, and the reply stops as `refused`.
- A reply that was not streamed, sent as `application/json`, is read as one piece.

| `finish_reason` | Stop reason |
| --- | --- |
| `stop` | `end`, or `tool_call` when the reply holds a tool call. Ollama has said `stop` for both. |
| `tool_calls`, `function_call` | `tool_call`, or `end` when no call arrived. |
| `length` | `max_tokens`. A tool call the limit cut off is dropped, and the text is kept. |
| `content_filter` | `refused`. |

| Usage | From |
| --- | --- |
| `input_tokens` | `prompt_tokens` less the cached part. |
| `cache_read_tokens` | `prompt_tokens_details.cached_tokens`, or, when that says nothing was cached, `prompt_cache_hit_tokens`, the form some endpoints use instead (since P5-03). Never more than the prompt. |
| `output_tokens` | `completion_tokens`. |
| `thinking_tokens` | `completion_tokens_details.reasoning_tokens`. |
| `estimated` | True when the endpoint reported no usage. The counts are then the bytes sent and received, at four bytes a token. |

`cache_write_tokens` is always zero: the format does not report it.

**Caching.** The adapter sends no cache marks and ignores a request's cache points ([caching.md](caching.md)). The endpoints of this kind cache by themselves where they cache at all: OpenAI's own endpoint and OpenRouter by the longest prefix they have seen, reported as above, and a local gateway such as the Mac mini's by keeping the prompt it last read and reusing its common start, which it does not report. The context builder's fixed order, with what changes on every turn last, is what lets each of them reuse a chat's start.

## When a call fails

A failed call ends with an `error` event, and `Stream` returns that `*seam.Error`.

| What happened | Kind | Retryable |
| --- | --- | --- |
| Status 401 or 403 | `auth` | No |
| Status 429 | `rate_limited` | Yes, unless the body says `insufficient_quota` |
| Status 503 or 529 | `overloaded` | Yes |
| Status 408, 500, 502, 504, any other 5xx | `unavailable` | Yes |
| Status 413, or any refusal whose words say the request does not fit the context window | `too_long` | No |
| Status 400, 404, 405, 409 or 422 | `invalid` | No |
| Any other status, 402 and a redirect included | `other` | No |
| The endpoint could not be reached, or did not answer in time | `unavailable` | Yes |
| The endpoint's certificate is not trusted | `unavailable` | No |
| The reply broke off, went quiet for longer than the idle timeout, or held a piece that is not JSON | `unavailable` | Yes |
| An error inside a reply that had begun | By its `code` when that is a status, else by its words, else `other` | As above |
| A tool call with no name, or with input that is not a JSON object | `other` | Yes |
| The request is not valid, or holds a block with no bytes | `invalid` | No |

The message gives the status and up to 300 characters of the endpoint's own reason. It never holds the key, and never the endpoint's address.

A redirect is not followed. The request, and what it carries, goes to the address in the entry and nowhere else.

### Retries

A call marked retryable is sent again, up to `chat_completions.max_attempts` times in all, but only while nothing has reached the caller. Once the first event has been sent on, a failure is reported and the caller decides, because what a person has already seen cannot be taken back.

The wait before the second attempt is `chat_completions.retry_wait`. It doubles with each attempt, is shortened by up to half at random, and is never more than `chat_completions.retry_wait_max`. If the endpoint sent `Retry-After` and it is longer, that is the wait, up to the same cap.

Cancelling the context ends a wait, a call and a reply at once, and `Stream` returns the context's error. When `emit` returns an error, `Stream` stops and returns it.

Decision [0122](decisions/0122-retries-stop-at-the-first-event.md) has the reasoning. Falling back to another model is routing's job (P3-08), and it reads the kind and `retryable` to decide.

## A local model and the proxy

An entry whose class is `local` is called directly. The proxy named in the environment, if any, is used only for `hosted` and `trusted` entries. Content that may be sensitive is allowed to reach a local model, so it must not pass through a machine in between.

## Settings

In `settings/models.json`. All are install-wide, and read by `adapters.ChatCompletionsOptions`, which passes them to the adapter. The adapter reads no setting itself.

| Key | Default | Meaning |
| --- | --- | --- |
| `chat_completions.connect_timeout` | `10s` | The longest wait to connect, and again to finish the TLS handshake. |
| `chat_completions.response_timeout` | `2m` | The longest wait for the endpoint to start its answer. A local model may be loading. |
| `chat_completions.idle_timeout` | `2m` | The longest wait between two pieces of a reply. |
| `chat_completions.max_attempts` | `3` | How many times one call is sent at most. One means no retry. |
| `chat_completions.retry_wait` | `500ms` | The wait before the second attempt. |
| `chat_completions.retry_wait_max` | `8s` | The longest wait between two attempts. |

## In code

```go
reg, err := settings.Registry()
set, err := adapters.ChatCompletions(reg)      // a Set holding this adapter alone
// whoever starts the server copies it into the set it passes to Open:
//   maps.Copy(all, set)
m, p, err := adapters.Open(ctx, store, set, "study-gateway")
reply, err := seam.Call(ctx, p, seam.Request{Model: m.Name, Messages: messages})
```

`chatcompletions.New(model, key, options)` is the adapter's own constructor, for a caller that has the entry and the key already.

A provider holds nothing that changes and is safe to use from several goroutines. It keeps a finished connection for 30 seconds for the next call to the same endpoint.

The SSE reader and the retry loop are this package's own. The other two adapters keep theirs. Nothing is shared between them yet.

## Checked by

`go test ./internal/adapters/chatcompletions`, with no key and no network. Every server is on this machine.

- A recorded exchange for each endpoint style, in `fixtures/chat-completions/`: the body sent is the one recorded, the `Authorization` header is there only when the entry has a key, and the reply adds up to the one recorded.
- The gateway's exchange runs over TLS with a certificate made when the test runs. A call fails with no `ca_cert` and with another authority's, and works with the right one as PEM text and as a file.
- The adapter's code does not name the switch that turns certificate checks off.
- Every refusal in `errors.json` becomes its seam error, and between them they cover all seven kinds.
- An error inside a reply ends it, and the call is not sent again.
- A busy endpoint is tried again and then answers. One that stays busy is given up on after the limit. A refused key is tried once.
- A reply that breaks off, an endpoint that is not there, and a reply that goes quiet are each `unavailable`.
- Cancelling stops the call with nothing sent after it. A receiver that goes away ends it.
- An endpoint that repeats the key in its refusal does not get it into the error.
- A local entry has no proxy. A hosted one has the environment's.
- A model added to a tenant's registry, with its key in the tenant's vault, is opened through `adapters.ChatCompletions` and answers.

In `verify`: the folder is on the `offline` check's list of folders that may import a network package.

## Not here yet

- **The live checks.** No real endpoint has been called. See below.
- **Asking for thinking.** `thinking_tokens` is not sent. The endpoints of this kind each ask for reasoning in their own way, and a field one of them does not know can be refused. Reasoning that an endpoint sends is read. A per-entry way to ask for it can be added when a model needs it.
- **Cache writes.** Not reported by the format.
- **Cache marks for an Anthropic model behind OpenRouter**, which takes `cache_control` on content parts. Without them such a model is cached only as far as OpenRouter does by itself.
- **Embeddings.** `/embeddings` is a separate call with its own request shape, and comes with P6-08.
- **Asking the endpoint what models it has.** An entry is written by hand.
- **OpenRouter's own headers and fields**, such as the site name and provider preferences. Not needed to make a call.
- **A repo-wide check for the certificate switch.** The test covers this adapter's folder. A standing check in `verify` could cover every folder.

## The owner gate

The item is provisional on three live checks, which need what only the owner has:

1. **The Mac mini gateway**: its address, the secret it expects, and its authority's certificate. With those, add an entry like the one at the top of this page and send one message with a tool.
2. **OpenRouter**: a key, stored in a tenant's vault, and an entry with `endpoint` set to OpenRouter's `/api/v1` address and a model name.
3. **Ollama**, optional: Ollama running on this machine with one model pulled, and an entry with `endpoint` `http://127.0.0.1:11434/v1` and no key.

What may differ from the recorded exchanges, and is worth watching on the first live call: whether the gateway reports usage and sends `[DONE]`, and what it calls reasoning.
