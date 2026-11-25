# P5-03. Prompt caching

Status: doing (chats)
Lane: chats
Depends on: P5-01, P3-04
Owner gate: none
Scope: Stable prefix order; cache markers on the Anthropic adapter; cache hits recorded in usage.
Proof: A second turn in a chat shows cache hits in its usage record.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What is there already: usage records, the audit log and the inspector carry cache read and write tokens, `Price.CostMicros` prices them, the Anthropic adapter reads `cache_read_input_tokens` and `cache_creation_input_tokens` and places two marks of its own (decision 0100), and the Responses and Chat Completions adapters read `cached_tokens`. What is missing is a call whose start stays the same from turn to turn, and marks where that start and the history end.

What changes:

- **The seam** (`internal/seam`). `Request` gains `Cache *Cache`, the call's cache points: `start`, how many system blocks are the standing start, and `history`, how many messages an earlier call of the same chat sent too. Checked to lie inside the request. Nil leaves the marks to the adapter as before, so title, summary and memory calls do not change.
- **The context builder** (`internal/contextbuild`). The request's order is fixed for caching, and the packing (what is kept and dropped) does not change:
  - tools, sorted by name, so a new tool never moves the others about;
  - system: the standing start (rules, profile, person map, project, style), then the parts that change rarely (attachments, summary, pinned messages, note);
  - messages: the earlier turns, then the current turn, with the recalled items as text blocks before the person's words in its first message. Recall leaves the system text, so the system text stays the same while a chat goes on, and so does every byte of the history.
  - `Record` gains `Order` (the parts sent, in the order the request carries them) and `Cache` (the points it set).
- **The Anthropic adapter.** With a request's cache points and `anthropic.cache_control` on, up to four marks: the end of the standing start (the last tool when the start is empty), the last system block when there are blocks after the start, the last block of the history, and the last block of the last message. A thinking block is never marked, and a message the adapter leaves out is allowed for. With no points, the two marks of decision 0100. The setting turns all of them off. New exchanges in `fixtures/anthropic/cache.json`.
- **Cache tokens from the other providers.** Chat Completions also reads `prompt_cache_hit_tokens`, the form some endpoints use instead of `prompt_tokens_details`. Responses already reads `cached_tokens`. The adapters' docs say what each provider caches by itself.
- **The fake provider.** A script's `user_text` also matches the last text block of the person's message, since recall now sits before the person's words in that message. Every match that held before still holds.
- **Usage, cost and the inspector** already carry and price the cache tokens and show them: proven end to end, no change planned.
- `internal/turn`: no change to its code; one new test file.

How it is proven:

- `go test ./internal/contextbuild`: the order (tools by name, the standing start, the rare parts, the turns, recall with the current message); over the turns of a growing chat with new recall each turn, the tools, the system text and the history are byte-identical to the call before; the record's order and points; the record still rebuilds the request.
- `go test ./internal/seam`: the points go through JSON and points outside the request are refused.
- `go test ./internal/adapters/anthropic`: the new exchanges (four marks; an empty start with tools; a history that ends in thinking; a message left out; the setting off).
- `go test ./internal/turn`, `cache_test.go`: three turns of one chat on a model behind the Anthropic adapter, against a stand-in Anthropic server on this machine that caches as the API does (by the bytes up to each mark, with a least length and a look back of 20 blocks). The second turn sends the first turn's prefix again, byte for byte, with its marks; the stand-in reads it from its cache; the turn's usage record shows the cache read tokens, priced at the cache price; the third reads more. The inspector's totals show them.
- `verify`, in `context fit`: for every fixture model, a chat that grows turn by turn keeps its tools, system text and history byte-identical from one call to the next, with recall only after the history.

Never called: the real Anthropic API.

### Left to do

Kept up to date at each commit, so another session can finish the item.

1. The seam's cache points and the builder's order, with tests.
2. The Anthropic adapter's marks from the points, with fixtures; the other adapters' cache tokens; the fake's match.
3. The proof in `internal/turn` and the step in `verify`.
4. Write-up: `context.md`, `adapter-anthropic.md`, `seam.md`, the other adapters' docs, `usage.md`; decisions from 0330; the journal `docs/journal/2026-11-25-chats-007.md`; the item's status and result.
