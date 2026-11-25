# Prompt caching

Folders: `internal/contextbuild` (the order), `internal/seam` (the cache points), `internal/adapters/anthropic` (the marks), `internal/adapters/chatcompletions` and `internal/adapters/responses` (the counts they read). Built in P5-03.

A long chat sends the same start on every turn: the tools, the standing instructions, the profile, the map, the summary and every earlier turn. A provider that caches that start charges a tenth of the input price to read it again, and a local gateway that keeps it does not have to read it again before it answers. Both need the start to be the same, byte for byte, from one call to the next. This page says how Keystone keeps it so, how it tells the Anthropic API where to cache, and where the counts of what was cached end up.

**Not yet proven against the real Anthropic API.** It is proven against a stand-in on this machine that caches by the published rules. The live check is in [adapter-anthropic.md](adapter-anthropic.md).

## The order of a call

The context builder ([context.md](context.md)) packs a call by priority, as before, and writes what it kept in a fixed order, `contextbuild.Layout`, with what changes least first (decision [0330](decisions/0330-what-changes-every-turn-goes-last-and-recall-goes-with-the-message.md)):

| Where | Parts | Changes when |
| --- | --- | --- |
| tools | every tool offered, sorted by name | the person switches tools on or off, or a tool is added |
| system, first | the standing start: rules (with the search-first rule), profile, person map, project, style | the person changes one of them, or the knowledge store is down for a turn |
| system, after it | attachments, the summary, pinned messages sent on their own, the note | compaction runs, a pin changes, or the first turn is left out |
| messages | the earlier turns, as the chat record holds them | a turn is left out at the start |
| messages, last | the current turn, with recall as text blocks before the person's words in its first message | every call |

So the system text stays the same while a chat goes on, and each call's history is the call before's history and its turn. In a turn of tool steps, each step sends the step before's whole call and the tools' results after it.

The builder's record says what it did: `Record.Order` lists the parts that were sent, in this order, and `Record.Cache` is the cache points below.

## The cache points

```go
type Cache struct {
	Start   int // system blocks, from the first, that are the standing start
	History int // messages, from the first, that an earlier call of the chat sent too
}
```

`seam.Request.Cache` carries them (decision [0331](decisions/0331-the-seam-carries-cache-points-and-the-anthropic-adapter-makes-four-marks.md)). The context builder sets them on every chat call: `Start` counts the standing start's blocks that were sent, and `History` is the index of the current turn's first message. `seam.ValidateRequest` refuses points outside the request. A request built anywhere else, such as a title, a summary or the memory pass, has none.

## The Anthropic marks

The Messages API caches only where a block carries `cache_control`, at most four places. It reads the tools, then the system blocks, then the messages, and a mark caches everything up to the end of its block. A later call finds a cached prefix only if every byte of it is the same, looking back from each of its own marks over about twenty blocks. A prefix shorter than the model's least (1024 tokens or more, by model) is passed over without an error.

With `anthropic.cache_control` on, the adapter marks:

| Mark | Where | Read again by |
| --- | --- | --- |
| 1 | the end of the standing start, or the last tool when the start is empty | every call of the chat, even after compaction |
| 2 | the last system block, when there are blocks after the start | every call until compaction runs again |
| 3 | the end of the history | the next turn, which reads all but the last turn |
| 4 | the end of the last message | the next step of a turn of tool calls, and the next turn when nothing was recalled |

Thinking blocks cannot carry a mark, so a mark goes on the block before one. A message left out of the call (thinking from another provider) is allowed for. A request with no points gets the two marks of decision [0100](decisions/0100-the-anthropic-adapter-places-cache-marks.md). The setting turns all marks off. The marks ask for the default lifetime, five minutes.

What a chat costs, turn by turn, with a long standing start and no recall: the first turn writes its whole call to the cache; the second reads the first's whole call and writes its own new turn; and so on. With recall, the second turn reads the standing start, and from the third each turn reads every turn but the last, since the last was sent with its recall.

## What the other providers do

Neither reads the cache points. Both cache by themselves.

- **OpenAI Responses** ([adapter-responses.md](adapter-responses.md)): the provider caches any prompt of 1024 tokens or more by its longest prefix, with no marks, and reports `input_tokens_details.cached_tokens`, which the adapter records as cache reads. It reports no writes.
- **Chat Completions** ([adapter-chat-completions.md](adapter-chat-completions.md)): OpenAI's own endpoint and OpenRouter cache by prefix where the model behind them does, and report `prompt_tokens_details.cached_tokens`; some endpoints, DeepSeek's among them, report `prompt_cache_hit_tokens` instead. The adapter reads both as cache reads. The format reports no writes. A local gateway such as the Mac mini's keeps the prompt it last read and reuses its common start without saying so in its usage: the order above is what lets it, and a reply begins sooner.

## Counts and cost

Each adapter turns its provider's counts into the seam's: `input_tokens` is what was neither read from nor written to the cache, and `cache_read_tokens` and `cache_write_tokens` are the rest ([seam.md](seam.md)). The turn writes all four into the usage record and the audit log, and `Price.CostMicros` prices them with the entry's `cache_read` and `cache_write` prices ([usage.md](usage.md), [models.md](models.md)). The spend cap counts the most a call could cost at the dearer of the input and cache-write prices (decision [0161](decisions/0161-the-cap-stops-a-call-that-could-pass-it.md)).

The turn inspector ([inspector.md](inspector.md)) shows each call's cache reads beside its tokens, and the turn's reads and writes under its totals. Each call's record carries its `order` and `cache` points.

## Settings

| Key | Default | What it does |
| --- | --- | --- |
| `anthropic.cache_control` | `true` | Whether the Anthropic adapter sends any mark. For the install. |

Nothing else is a setting: the order and the points follow from the parts, and the four marks are the API's limit.

## Checks

- `go test ./internal/contextbuild` (`cache_test.go`): over six turns of a growing chat, with tools given in another order and new recall each time, the tools, the whole system text and every earlier turn are the bytes the call before sent; the person's message of the call before follows its history as written; recall is in the current message only; the points and the order are recorded. Each step of a turn of tool calls sends the step before's start. The points when turns are left out, and a new tool moves no other.
- `go test ./internal/seam` (`cache_test.go`): the points go through JSON, and points outside the request are refused.
- `go test ./internal/adapters/anthropic`: `fixtures/anthropic/cache.json`, five exchanges (the four marks; an empty start that marks the last tool; a history that ends in thinking; a message left out before the history mark; the setting off), and `cache_test.go`, which builds several hundred requests of every shape and finds never more than four marks, none on thinking, and the standing start and the last message always marked.
- `go test ./internal/adapters/chatcompletions` (`cache_test.go`): both forms of the cached count, and a count larger than the prompt.
- `go test ./internal/turn` (`cache_test.go`), the item's proof: a model on the Anthropic adapter, the adapter built from the shipped settings, against a stand-in Messages API on this machine that caches as the API does (by the bytes up to each mark, a least of 1024 tokens, a look back of twenty blocks). Three turns of one chat: the second sends the first's whole call again, byte for byte, with its marks; the stand-in reads it from its cache; the turn's usage record shows the tokens read and costs what its counts cost at the cache prices, less than with no cache; the third reads more; the inspector's totals show the reads. A second test with recall from a knowledge store: the second turn still reads the standing start, and the third reads the second's history.
- `verify`, `context fit` (`tools/verify/cacheorder.go`): for every fixture model, a chat that grows by one message at a time keeps its tools, system text and history byte for byte from one call to the next, with recall in the current message only ([tools.md](tools.md)).

## Not here yet

- The live check against the Anthropic API: a second turn of a real chat showing cache reads in the inspector.
- A longer cache lifetime (one hour), which costs more to write and would suit a chat a person comes back to.
- `prompt_cache_key` for OpenAI, which keeps a chat's calls on the same machine.
- Marks for an Anthropic model reached through OpenRouter's Chat Completions format, which takes `cache_control` on content parts.
- Tool definitions sent as names only until one is needed (P7-05): when it comes, a tool that is sent in full must stay in full, or the start changes.
