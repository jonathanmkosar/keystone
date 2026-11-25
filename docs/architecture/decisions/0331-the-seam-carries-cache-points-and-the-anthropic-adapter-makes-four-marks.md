# 0331. The seam carries a request's cache points, and the Anthropic adapter turns them into at most four marks

November 25, 2026 · decided in P5-03

## The question

The Anthropic Messages API caches only where a block carries `cache_control`, at most four places in a request. Decision 0100 had the adapter guess: the last system block and the last message. With recall moved into the current message (decision 0330) the adapter cannot tell where the stable part of a request ends: it does not know which system blocks are the standing start, nor where the earlier turns end and the current turn begins. Who says where the marks go, and how does it reach the adapter?

## The decision

**`seam.Request` has an optional `Cache`, the request's cache points:** `start`, how many system blocks are the standing start, and `history`, how many messages an earlier call of the same chat sent too. They are counts, not marks on blocks. The context builder sets them on every chat call. `ValidateRequest` refuses points outside the request.

**The Anthropic adapter places up to four marks from them,** with `anthropic.cache_control` on:

1. the end of the standing start, or the last tool when the start is empty;
2. the last system block, when there are blocks after the start;
3. the end of the history;
4. the end of the last message.

A thinking block cannot carry a mark, so a mark that would fall on one goes on the block before it. A message the adapter leaves out (thinking from another provider, with no signature) is allowed for when the history is counted. A request with no points, such as a title, a summary or the memory pass, gets the two marks of decision 0100. The setting turns every mark off.

The other adapters read no points: their providers cache by themselves.

## Why

- Counts say what the builder knows, where the stable parts end, and nothing about how a provider marks them. The seam stays free of one provider's notion, and `seam.Block`, which the chat record stores and every lane uses, does not change.
- Mark 1 keeps the standing start when anything after it changes: the summary after compaction, the note when the first turn is left out.
- Mark 2 keeps the system text whole when the history changes at its start, and when a turn has more blocks than the API looks back over (twenty).
- Mark 3 is what each turn's next turn reads: everything but its last turn.
- Mark 4 is what the next step of a turn of tool calls reads, and the next turn too when nothing was recalled. It writes the current turn at the price of a write on every call. The current turn is short beside the history, and a turn that calls tools reads it back at a tenth of the price, so it is kept.
- Four is the API's limit, and the four are always within it.

## What it costs

- A prefix shorter than the model's least is passed over by the API without an error, so a short chat with short instructions caches nothing. The least differs by model, and the adapter does not count to skip a mark: an unused mark costs nothing.
- Mark 4 is a write the next turn cannot read when recall was in the current message. At the API's prices that is a quarter of the current turn's input more, once.
- The marks ask for the provider's default lifetime of five minutes. A chat left longer than that starts its cache again. A longer lifetime is not built.
- The setting is for the install, as before (decision 0100). A gateway that refuses the field needs it off for everyone.
