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
| `Summary` | The summary of older history, with its heading | P5-02 |
| `SummaryCovers` | How many of `Messages`, from the first, the summary stands for | P5-02 |
| `Pinned` | The messages the person pinned, each a `Pin`: its index in `Messages` and an `Item` | P5-02 |

The first seven are filled, and so are `Profile` and `PersonMap` (P6-04), `Recall` ([knowledge-recall.md](knowledge-recall.md)), and `Summary`, `SummaryCovers` and `Pinned` ([compaction.md](compaction.md)). The others are empty, and an empty part takes no room and is listed in the record as `empty`. An item that fills one changes its caller and nothing in this package. An `Item` is a label for the inspector, the text that is sent, and `Pinned`.

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
| `recall` | the current turn's first message, before the person's words (since P5-03) | up to `chats.context_recall_percent`; an item that does not fit is left out |
| `summary` | system | sent whole within `chats.context_summary_percent`, or not at all. When it is sent, the turns it covers are not |
| `pinned` | system | never left out: a pinned message whose turn is not sent is sent on its own |
| `note` | system, last | sent only when turns were left out |

The first six are the standing start of a call. They are always the first system blocks, in the same order, and do not change with the length of the chat, so a provider can cache them (the plan's rule). What varies comes after them.

A pinned item is counted with the parts that are never left out.

### The order of the request

Since P5-03 the request carries the parts in another order than the record lists them, `Layout`, with what changes least first, so that a provider can cache the start of every call ([caching.md](caching.md), decision [0330](decisions/0330-what-changes-every-turn-goes-last-and-recall-goes-with-the-message.md)):

1. `tools`, sorted by name (`ByName`), whatever order they were given in;
2. the standing start: `rules`, `profile`, `person_map`, `project`, `style`;
3. the system blocks that change rarely: `attached`, `summary`, `pinned`, `note`;
4. `recent`, the earlier turns;
5. `recall`, as text blocks at the start of the current turn's first message, and `current`.

So the system text stays the same while a chat goes on, and so does every earlier turn. The request's cache points, `seam.Request.Cache`, say where the standing start ends (`Start`, in system blocks) and where the earlier turns end (`History`, in messages). The packing is as before: only where each kept piece is written moved. The branch the builder is given is not changed: recall goes into a copy of the current turn's first message.

## How it packs

1. **The room.** `budget.InputRoom(model, maxOutput)` is the window less what is kept for the reply. The limit is that room times `chats.context_fill_percent`.
2. **Weigh.** Every piece is measured once with `budget.Estimate`: each system text, the tools, each item, each turn.
3. **What is never left out** goes in: the standing start, the tools, pinned items and the current turn. What is left is the open room, and every share below is a percentage of it.
4. **Attachments**, in the order given, up to their share.
5. **Earlier turns**, from the newest back, in what is left after a share is held for recall and one for the summary (decision [0171](decisions/0171-recall-and-the-summary-hold-a-share.md)). The first turn that does not fit ends this step. No older turn is sent past a gap, so the conversation the model reads has no holes. `chats.context_recent_turns_max` caps the number.
6. **Recall**, most relevant first, up to its share. An item too large for what is left is passed over and a smaller one after it may still go.
7. **The summary**, if it fits its share. When it does, the earlier turns that lie wholly inside `SummaryCovers` are not candidates in step 5: the summary is sent in their place, and the note is sent only for turns left out after them. When it does not, they are packed as any other turn. Pinned messages are held their room in step 3; one whose turn step 5 sent gets its room back and is not sent twice, and the others are sent on their own after the summary (decision [0233](decisions/0233-a-pinned-message-is-sent-on-its-own-only-when-its-turn-is-not.md)).
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

Since P5-02 the record also has `TurnRoom`, the room the earlier turns were packed in, and `SummaryRoom`, the most the summary could take, both in estimated tokens with no margin; compaction measures against them ([compaction.md](compaction.md)). A `TurnRecord` says `summarised` when the summary was sent in its place, and such turns are counted neither kept nor dropped in the `recent` part. The `summary` part's `covers` is how many turns it was sent in place of. The `pinned` part lists every pin, each `kept` when it was sent on its own and `in_turn` when its turn was sent.

Since P5-04 an `Item` also has a `Ref`, the turn inspector's name for it (`page:7`, `source:3`, `message:42`), and `Removed`, which the person sets from the inspector for one turn. A removed item is not sent and takes no room, even when it is pinned, and its `ItemRecord` says `removed` with the tokens it would have taken; a `PartRecord` counts such items in `removed`, apart from kept and dropped, and a part whose items were all removed is `dropped`. A removed pin is held no room and not sent on its own; when its turn is sent it is in the call in its turn, and its record says `in_turn`. `Input.SummaryRef` names the summary, and the summary part carries it as `ref`. See [inspector.md](inspector.md) and decision [0272](decisions/0272-a-removed-item-is-marked-and-recorded-and-never-moves-a-call.md).

Since P5-03 the record also has `Order`, the parts that were sent in the order the request carries them (`Layout` less what sent nothing), and `Cache`, the request's cache points. The `recall` part's place is `messages`.

Part and turn tokens are estimates with no margin. `Tokens` is the count the budget checks.

The record is the request: the tests put the request together again from the record and the input alone and compare the two.

### Where it is kept

The turn runner stores the record of every model call in `turn_contexts` (migration `20261125T070000_turn_contexts.sql`): the turn, the step, and the record as JSON. `turn.Contexts(ctx, store, turnID)` reads them back in order. That is what the turn inspector (P5-04) will show. The rows go when the turn's chat is deleted. A record that cannot be stored is logged and does not stop the call, because the audit log still holds what was sent.

Since P9-02 the turn fills `Attached`: `Runner.withUploads` (`internal/turn/uploads.go`) readies the branch's images and files for the call's route before `Build`, and gives the text of the chat's text files as items, the newest file first, each labelled `file: <name>` with the ref `upload:<id>`. They are packed within `chats.context_attached_percent` like any attachment and listed in the record with their tokens ([uploads.md](uploads.md)).

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
- Since P5-02, `go test ./internal/contextbuild` also proves the summary and pinned messages (`compaction_test.go`), and `context fit` grows a chat to ten times each window with a summary and pins ([compaction.md](compaction.md)).
- Since P5-03, `go test ./internal/contextbuild` proves the order (`cache_test.go`): over the turns of a growing chat with new recall each time, the tools, the system text and the history are the bytes the call before sent, and recall is in the current message only. `context fit` checks the same for every fixture model ([caching.md](caching.md)).

## Not here yet

- Every slot but the rules, the tools, the current turn, the earlier turns, the profile, the person map, recall, the summary and pinned messages is empty: attachments and attached chats (P5-06), project (P9-01), style (P9-05).
- Since P7-01 the turn fills `Tools` with every tool in the server's registry, for a model with the `tools` capability, unless the person has switched tools off ([tool-loop.md](tool-loop.md)).
- Cache marks on the standing start (P5-03). Built since: recall moved into the current turn's first message, the system blocks after the standing start change only when compaction runs or turns are left out, and the request carries cache points the Anthropic adapter marks ([caching.md](caching.md)).
- The inspector's route and screen (P5-04). The records are stored and `turn.Contexts` reads them. Built since: `GET /chats/{id}/turns/{turn}/inspect` returns them with each call's usage records ([inspector.md](inspector.md)).
- Tool definitions as names only until one is needed (P7-01 with P7-05). Since P7-04 a connector's tools are offered so, by name and one line until `load_tools` loads them ([connectors.md](connectors.md)); the server's own tools and the lookups are still offered whole.
- A relevance threshold for recall is the recall item's own (P6-05): the builder takes the items it is given.
- A current turn that outgrows the window on its own, as a long run of tool calls could, is refused whole. Trimming inside a turn is the tool loop's question.
