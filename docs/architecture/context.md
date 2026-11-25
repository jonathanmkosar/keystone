# Context builder

Folder: `internal/contextbuild`

Builds what one model call carries. Given the model, the chat's current branch and the parts a call may hold, `Build` returns a `seam.Request` that fits the model's context window, and a record of what went in, what was left out and how many tokens each part took.

The package keeps nothing, opens no connection and reads no store. It imports the seam ([seam.md](seam.md)), the budget ([budget.md](budget.md)), the registry's `Model` ([models.md](models.md)) and the settings registry. The turn runner calls it once for every model call ([turns.md](turns.md)).

## The function

```go
func Build(ctx context.Context, in Input) (seam.Request, Record, error)
```

`Input` holds:

| Field | What it is | Filled by |
| --- | --- | --- |
| `Model` | The registry entry the call goes to | the turn |
| `Budget` | How tokens are estimated: `budget.Options` | the turn |
| `Options` | This package's settings, below | the turn |
| `MaxOutputTokens` | The room held back for the reply. Zero means the model's output limit | the turn |
| `ThinkingTokens` | Passed to the request as it is | the turn |
| `Provider` | Asked to count the packed request when it is a `budget.Counter`. May be nil | the turn |
| `Messages` | The chat's current branch, oldest first, as a provider takes it | the turn |
| `Tools` | The tools offered | P7-01 |
| `Profile`, `PersonMap` | What Keystone knows of the person, and the map of their world | P6-04 |
| `Project` | The project's instructions | P9-01 |
| `Style` | The style the person chose | P9-05 |
| `Attached` | Attachments and attached chats, as items, the most wanted first | P5-06 |
| `Recall` | Recalled knowledge, as items, the most relevant first | P6-05 |
| `Summary` | The summary of older history | P5-02 |

Only the first seven are filled today. The others are empty, and an empty part takes no room and is listed in the record as `empty`. An item that fills one changes its caller and nothing in this package. An `Item` is a label for the inspector, the text that is sent, and `Pinned`.

The only errors are a branch with no messages (`ErrNoMessages`), options that are not valid, and a cancelled context.

## The parts and their order

The plan fixes the order of priority: rules and tools, the current message, attachments and attached chats, recent turns, recalled knowledge, older history as a summary. The parts, in the order the record lists them:

| Part | Where it goes | When room is short |
| --- | --- | --- |
| `rules` | system, first | never left out |
| `tools` | the request's tools | never left out |
| `profile`, `person_map`, `project`, `style` | system, after the rules, in this order | never left out |
| `current` | the last messages | never left out |
| `attached` | system, after the standing start | up to `chats.context_attached_percent`; an item that does not fit is left out |
| `recent` | messages, before the current turn | the oldest turns are left out, whole |
| `recall` | system | up to `chats.context_recall_percent`; an item that does not fit is left out |
| `summary` | system | sent whole within `chats.context_summary_percent`, or not at all |
| `note` | system, last | sent only when turns were left out |

The first six are the standing start of a call. They are always the first system blocks, in the same order, and do not change with the length of the chat, so a provider can cache them (the plan's rule; placing cache marks is P5-03). What varies comes after them.

A pinned item is counted with the parts that are never left out.

## How it packs

1. **The room.** `budget.InputRoom(model, maxOutput)` is the window less what is kept for the reply. The limit is that room times `chats.context_fill_percent`.
2. **Weigh.** Every piece is measured once with `budget.Estimate`: each system text, the tools, each item, each turn.
3. **What is never left out** goes in: the standing start, the tools, pinned items and the current turn. What is left is the open room, and every share below is a percentage of it.
4. **Attachments**, in the order given, up to their share.
5. **Earlier turns**, from the newest back, in what is left after a share is held for recall and one for the summary (decision [0171](decisions/0171-recall-and-the-summary-hold-a-share.md)). The first turn that does not fit ends this step. No older turn is sent past a gap, so the conversation the model reads has no holes. `chats.context_recent_turns_max` caps the number.
6. **Recall**, most relevant first, up to its share. An item too large for what is left is passed over and a smaller one after it may still go.
7. **The summary**, if it fits its share.
8. **The note.** When any turn was left out, `chats.context_dropped_note` is sent as the last system block, and its tokens are found by leaving out one more turn if need be.
9. **Count.** `budget.Tokens` counts the packed request: the provider's own count where it offers one, the estimate with its margin elsewhere. When a provider counts more than the limit, the request is packed again under a target made smaller by as much as the provider counted more, up to `chats.context_recount_max` times (decision [0172](decisions/0172-the-builder-packs-by-estimate-and-asks-the-provider-once.md)).

### A turn is whole

A turn is a message of the person's and everything up to their next: the reply, and in a turn that used tools the calls, the results and the replies after them. `Turns(messages)` cuts a branch into turns. A turn starts at a message of the person's that holds no tool result, and only where no tool call is waiting for its result. So a call and its result are always in one turn, and leaving a turn out never parts them (decision [0170](decisions/0170-a-turn-is-kept-whole-or-left-out.md)).

### When even the required parts do not fit

The request is returned as it is: the standing start, the tools, pinned items and the current turn. `Record.Over` says by how many tokens. `Build` does not fail, and it cuts nothing out of the person's message. `budget.Guard` then refuses the call as `too_long`, as it refuses any call that cannot fit, and the turn ends with that error.

## The record

```go
type Record struct {
	Model    string
	Window   int // the model's context window
	Output   int // kept for the reply
	Room     int // budget.InputRoom
	Limit    int // what the builder packed under
	Tokens   int // the request's input tokens, as budget counts them
	Margin   int // the part of Tokens that is the estimate's margin
	Exact    bool // the provider counted
	Over     int // tokens over Limit; zero when it fits
	Recounts int
	Parts    []PartRecord // every part, in the fixed order
	Turns    []TurnRecord // every turn of the branch, oldest first
}
```

A `PartRecord` has the part's name, its place (`system`, `tools` or `messages`), its state (`empty`, `kept`, `partly`, `dropped`), the tokens of what was sent, how many pieces were kept and dropped, the tokens of what was dropped, and for attachments and recall each item with its label, tokens, and whether it was kept or pinned. A `TurnRecord` has the index of the turn's first message in `Input.Messages`, how many messages it has, its tokens, whether it is the current turn, and whether it was sent.

Part and turn tokens are estimates with no margin. `Tokens` is the count the budget checks.

The record is the request: the tests put the request together again from the record and the input alone and compare the two.

### Where it is kept

The turn runner stores the record of every model call in `turn_contexts` (migration `20261125T070000_turn_contexts.sql`): the turn, the step, and the record as JSON. `turn.Contexts(ctx, store, turnID)` reads them back in order. That is what the turn inspector (P5-04) will show. The rows go when the turn's chat is deleted. A record that cannot be stored is logged and does not stop the call, because the audit log still holds what was sent.

## Settings

All in `settings/chats.json`, and all a person may set for themselves. The runner reads them for each call.

| Setting | Default | What it does |
| --- | --- | --- |
| `chats.context_system_text` | two sentences | The standing instructions every chat call starts with. Empty sends none |
| `chats.context_fill_percent` | 100 | The share of the input room a call may fill |
| `chats.context_recent_turns_max` | 0 | The most earlier turns one call carries. Zero means as many as fit |
| `chats.context_attached_percent` | 40 | The most attachments may take of the open room |
| `chats.context_recall_percent` | 15 | The most recall may take of the open room, held for it ahead of earlier turns |
| `chats.context_summary_percent` | 10 | The same for the summary |
| `chats.context_dropped_note` | two sentences | What the model is told when turns were left out. Empty tells it nothing |
| `chats.context_recount_max` | 3 | How many times a call is packed again when the provider counts more than the estimate |

`FromSettings(registry, value)` reads them, asking `value` for a tenant's own first. `CheckOptions` refuses a fill outside 1 to 100, a negative number, and shares for recall and the summary that are more than 100 together.

## In the turn

`Runner.work` reads the branch as before (`buildMessages` still leaves out what no provider takes), and calls `Runner.build` in `internal/turn/context.go`, which reads the person's settings, calls `Build` with the provider the turn opened, and stores the record. The title call is not built here: it is one short message with its own prompt.

## Checks

- `go test ./internal/contextbuild`: a chat that fits is sent whole; the oldest turns are left out whole and with no gap; a tool call is never parted from its result, at every window from almost nothing to everything; the current turn and the standing start are never left out; the parts are packed in their order, with shares, pinned items and items passed over; the record counts what was sent; a provider's count is believed; the start of a call does not change with the chat.
- `go test ./internal/turn`: a chat several times its model's window is answered, the stored record names what was left out, and the audit log's copy of what was sent agrees with it.
- `verify`, `context fit`: for every fixture model, the largest test chat and a generated chat four times the window are each packed under the window, by the estimate and by a provider that counts half as much again ([tools.md](tools.md)).

## Not here yet

- Every slot but the rules, the current turn and the earlier turns is empty: tools (P7-01), profile and person map (P6-04), recall (P6-05), the summary and compaction (P5-02), attachments and attached chats (P5-06), project (P9-01), style (P9-05).
- Since P7-01 the turn fills `Tools` with every tool in the server's registry, for a model with the `tools` capability, unless the person has switched tools off ([tool-loop.md](tool-loop.md)).
- Cache marks on the standing start (P5-03). The varying system blocks sit after the standing start today. P5-03 may move them into the messages if a provider's cache needs the whole system text to stay the same.
- The inspector's route and screen (P5-04). The records are stored and `turn.Contexts` reads them.
- Tool definitions as names only until one is needed (P7-01 with P7-05).
- A relevance threshold for recall is the recall item's own (P6-05): the builder takes the items it is given.
- A current turn that outgrows the window on its own, as a long run of tool calls could, is refused whole. Trimming inside a turn is the tool loop's question.
