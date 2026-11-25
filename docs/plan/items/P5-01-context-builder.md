# P5-01. Context builder

Status: doing (chats)
Lane: chats
Depends on: P4-03, P3-07
Owner gate: none
Scope: Rebuilt every turn in the fixed priority order; a record of every section for the inspector.
Proof: Packing tests per priority; the record matches what the model received.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What changes:

- A new package, `internal/contextbuild`, with one function, `Build`. It is given the model, the budget options, its own options from settings, the chat's current branch as seam messages, and the parts a call may carry. It returns a `seam.Request` and a `Record`.
- The parts, in the plan's fixed order: rules (system text from a setting) and tools, the current message, attachments and attached chats, recent turns, recalled knowledge, older history as a summary. The stable start of a call also has named slots for the profile, the person map, the project and the style. Today only rules, the current message and recent turns have content. Every other part is a named, empty field of the input, packed by the same code, so P5-02, P5-06, P6-04, P6-05, P9-01 and P9-05 fill a field and change nothing else.
- Packing: the required parts first (the stable start, the tools, the current turn), then attachments, then recent turns newest first, then recall, then the summary, each inside what is left of `budget.InputRoom`. A turn is kept whole or dropped whole, and a turn is cut only where no tool call is waiting for its result. Once a turn does not fit, no older turn is kept, so the conversation sent has no holes.
- The count is `budget.Estimate` with the margin, part by part. When the provider can count, the packed request is counted once more by the provider, and repacked smaller if the provider's count is over.
- The record lists every part with its tokens and what was kept and dropped, and every turn of the branch with its tokens and whether it was sent. It is stored for each model call of a turn in a new table, `turn_contexts`, for the turn inspector (P5-04).
- `internal/turn`: the one line in `Runner.work` that builds the request becomes a call to a new method in a new file. No other existing line of the package changes.
- Tunables in `settings/chats.json`, all tenant scope so the runner reads them the way it reads the other per-person values.
- `context fit` in `verify` gains the packing step: the largest test chat, and a generated chat many times over the window, are each packed under the window of every fixture model.

How it is proven:

- Packing tests, one per priority: what is dropped first, what is never dropped, turns whole, a tool call never without its result, no holes.
- The record matches what the model received: the request is rebuilt from the record and the input and compared, and the parts' tokens are checked against `budget.Estimate` of the request.
- A turn test: a chat longer than its model's window gets an answer, not `too_long`, and the stored record names the dropped turns.
- `go run ./tools/verify` green, with the new step in `context fit`.

