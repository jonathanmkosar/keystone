# Chat Completions recorded exchanges

Test data. Everything in this folder is invented: the models, the providers, the addresses, the questions, the answers, the ids and the token counts. No address here leads anywhere, and no file holds a key or a certificate. Each file says so itself with `"test_data": true`.

These are recorded exchanges for the Chat Completions adapter, written in the shape each kind of endpoint sends. The tests in `internal/adapters/chatcompletions` replay them from servers on this machine. The adapter is described in [`docs/architecture/adapter-chat-completions.md`](../../docs/architecture/adapter-chat-completions.md).

| File | Style | Shows |
| --- | --- | --- |
| `ollama-plain.json` | Ollama | No key. A text answer in pieces, and the usage in a piece of its own. |
| `ollama-tool-call.json` | Ollama | A tool call sent whole in one piece, in a reply that says it finished with `stop`. |
| `openrouter-two-tools.json` | OpenRouter | A key. Comment lines, reasoning, text, two tool calls whose arguments arrive in pieces, and usage with cached and reasoning tokens. |
| `litellm-tool-result.json` | LiteLLM | A key. A request that carries a tool result, an image and a text file, and a reply with `reasoning_content`. |
| `gateway-cut-short.json` | The home gateway | A key, and https with the gateway's own certificate authority. A reply that stops at the output limit, with no usage reported. |
| `errors.json` | All | Refusals by status and body, and errors that arrive inside a reply, each with the seam error it must become. |
| `embeddings/exchanges.json` | Ollama, LiteLLM, the gateway | Embedding calls (P6-08): three hand-written exchanges of `POST /embeddings` (indexes in order and out of order, none at all; usage as prompt tokens, total tokens, or missing), four refusals with the seam error each becomes, and six answers that are not one usable vector for each text. In a folder of its own, since its shape is not a chat exchange's. |
| `effort/exchanges.json` | Any | A request's effort (P14-01): the request, the entry's capabilities, `chat_completions.send_reasoning_effort`, and the body that must be sent. Each level with the setting on, effort beside a thinking budget, the setting off, and an entry without `thinking`. Every one is answered by the same short stream in the test. |
| `golden/bodies.json` | All | Not an exchange: the exact body the adapter sent for each recorded exchange before effort existed. A request with no effort must still be sent as those bytes (`golden_test.go`). |

## An exchange

| Field | Holds |
| --- | --- |
| `style` | `ollama`, `openrouter`, `litellm` or `gateway`. A `gateway` exchange is replayed over TLS, with a certificate made when the test runs. |
| `entry` | The registry entry. The test puts its own server's address in `endpoint`. |
| `uses_key` | Whether the call must carry an `Authorization` header. |
| `request` | The call in Keystone's own format. |
| `sent` | The JSON body the adapter must send for it. |
| `stream` | The reply. An object is one `data:` event. A string is a line sent as it is: a comment, or `data: [DONE]`. |
| `response` | The reply in Keystone's own format, which the stream must add up to. When its usage is marked `estimated`, the test checks that the adapter's is too, and not the numbers. |

## Adding one

1. Copy the exchange nearest to what you need, and give it a `name` no other has.
2. Write the request, the body it becomes, the stream and the reply.
3. Run `go test ./internal/adapters/chatcompletions`.

Use invented content only. Nothing here may be a real name, address, account, key or certificate. An exchange captured from a real endpoint is rewritten with invented content before it is added.
