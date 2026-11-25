# Compaction

Folder: `internal/compact`, joined to a turn by `internal/turn/compact.go`. Pins are the chat record's (`internal/chat/pins.go`), with routes in `internal/turn/pins.go`.

Keeps a long chat inside its model's window. When a chat's earlier turns outgrow the room a call has for them, the oldest are summarised by the model routing gives the `summary` task, and later calls carry the summary in their place. A summary is extended as the chat grows, never made again from the start. The messages a person pinned are never left out.

The package reads no setting and opens no connection: the turn reads the settings and passes them in, and makes the model call through its route. It imports the seam and the tenant handle and nothing else of Keystone's. It says whether a summary of sensitive content may reach a call, so it is a guard of fixed rule 3.

Decisions: [0230](decisions/0230-a-summary-is-made-beside-the-turn.md) for making the summary beside the turn, [0231](decisions/0231-a-summary-extends-the-one-before.md) for extending it and what it stands for, [0232](decisions/0232-when-a-chat-is-summarised.md) for when, [0233](decisions/0233-a-pinned-message-is-sent-on-its-own-only-when-its-turn-is-not.md) for pinned messages, and [0234](decisions/0234-a-held-chats-summary-is-sensitive.md) for sensitive content.

## One chat, growing

1. **A call is built.** Before the context builder packs it, the turn finds the newest summary that stands for the start of the branch being sent, and the pinned messages on that branch. The builder sends the summary in its slot in place of the turns it covers, and each pinned message whose turn is not sent on its own ([context.md](context.md)).
2. **The record is read.** After the call is built, `compact.Plan` reads the record's earlier turns and the room they had (`TurnRoom`). When the turns the summary does not yet stand for take more than `chats.compact_trigger_percent` of that room, or one was left out, the oldest are to be summarised until the rest take no more than `chats.compact_keep_percent`.
3. **A job starts beside the turn.** The chat call is sent at once, with what was stored before. The job runs in the background with a store of its own, one at a time for a chat.
4. **The summary call.** The job opens the route for the `summary` task: `routing.summary_model`, then the chat's own model, then the fallback chain, each behind the budget's guard and the monthly cap ([routing.md](routing.md)). It sends the summary so far and the turns that follow it, as many as the first model's window holds, and stores the answer as a new summary that extends the one before. It does this up to `chats.compact_calls_max` times.
5. **The next call** carries the new summary. Its turns that the summary stands for are not sent.

When anything in the job fails, it writes one line in the log, stores nothing, and the chat is not tried again for `chats.compact_retry_after`. The chat goes on as it did before compaction: the oldest turns left out with the note.

## The package

| Function | Does |
| --- | --- |
| `Plan(turns, room, options)` | Which earlier turns to summarise next, as a `Span` of indexes, or nothing. `turns` are `compact.Turn`s: tokens, whether sent, whether the summary stood for it. |
| `Request(ask)` | The request that asks for the summary, and the turn it ends before. Takes in as many turns of the span as `ask.Fits` says the summary model's window holds, at least one; a turn too large on its own is cut and says so. |
| `Answer(response)` | The summary from the model's reply, or `ErrEmpty`. |
| `Save`, `List` | Keep a summary; read a chat's summaries, the one that stands for the most first. |
| `Usable(summary, mayReadSensitive)` | Whether a summary may go to a call: a sensitive one only when every model of the call may read sensitive content. |
| `CheckOptions(options)` | Refuses settings nothing can be summarised with, with `ErrOptions` and the setting's name. |

### The request

The system text is `chats.compact_prompt`, with `{words}` replaced by the most words the summary may have: the smaller of `chats.compact_summary_max_tokens` and the summary's share of the call (`Record.SummaryRoom`, less the heading), turned into words at the budget's characters per token and six characters to a word. The reply may be that many tokens long.

The one message holds the summary so far, or says there is none, then the turns, oldest first, written as plain text:

    The summary so far:
    The person is planning a walk by the harbour ...

    The turns that follow it, oldest first:

    The person: Which path is shorter?
    The assistant: The harbour road, by about a mile.
    The person: And by the cliff? [The assistant used the tool tide_table with {"day":"monday"}.] [The tool tide_table answered: High water at six.]

Thinking is left out. Each message is cut to `chats.compact_message_max_chars`. An image or a file is named and not sent.

## Stored with the chat

`chat_summaries`, from the migration `20261125T110000_compaction.sql`. One row for each summary:

| Column | Holds |
| --- | --- |
| `chat_id` | The chat. The rows go with it. |
| `first_id`, `through_id` | The first and the last message it stands for. Messages are a tree, so the path down to `through_id` is one branch, and the summary stands for all of it. |
| `turns` | How many turns it stands for. |
| `extends` | The summary it was made from; 0 for the first of a chain. |
| `text`, `model` | The summary, and the model that wrote it. |
| `sensitive` | The chat was held when it was made. |
| `tokens`, `turn_id`, `created_at` | Keystone's estimate of the text, the turn it was made beside, when. |

Rows are added and never changed. A chat whose person edits back before a summary's last message gets a chain of its own for the new branch, and finds the first chain again on the old one.

### Which summary a call gets

`olderHistory` in `internal/turn/compact.go`, for each model call:

1. The chat's summaries, the one that stands for the most first, are read when `chats.compact` is on.
2. The branch down to the message before the person's message is read. A summary whose `through_id` is not on it is passed over, and so is one that `Usable` refuses for the call's route.
3. The messages it stands for are built as the call sends them, with the same function the turn uses. Only when they are exactly the call's first messages, and end where a turn begins, is it used, with how many messages it covers. So a summary is never sent for messages built another way.
4. The text sent is `chats.compact_summary_heading`, a blank line, and the summary.

A summary that does not fit its share of a call is not sent, and the turns it covers are packed as any other, with the note. The next job extends it, and its request asks for fewer words.

## Pinned messages

`chat.Pin(store, chatID, messageID, pinned)` pins a message, or takes the pin off; `chat.Pins(store, chatID)` lists them, oldest first. A message on any branch may be pinned. The rows are `chat_pins`, from the same migration, and go with their chat.

The routes, in the shell like every chat route ([turns.md](turns.md)):

| Route | Reply |
| --- | --- |
| `GET /chats/{id}/pins` | `PinsReply`: `chat_id`, `pinned` (message ids, oldest first) |
| `PUT /chats/{id}/pins/{message}` | `PinsReply`, after pinning. No body. |
| `DELETE /chats/{id}/pins/{message}` | `PinsReply`, after taking the pin off. |

A chat or message that is not the person's is `not_found`.

Each call is given the pinned messages on its branch before the current turn, each as its text under `chats.context_pinned_heading` and a line saying who wrote it, with its place in the call's messages. The context builder holds room for each, gives the room back when the message's turn is sent, and otherwise sends it on its own in the `pinned` part. It is never left out: a pin too large for the window makes the call too long.

## Sensitive content

Fixed rule 3, twice:

1. **Who makes it.** The summary route is planned like the chat's own: when `recall.Held` says the chat was sent sensitive content, routing keeps only models that may read it ([knowledge-recall.md](knowledge-recall.md)). A hosted summary model is left out.
2. **Who is sent it.** The summary is stored as sensitive when the chat was held, read before and after the call. A call is given a sensitive summary only when every model of its route may read sensitive content, the same least-of-the-route answer recall uses.

`internal/compact` imports neither `settings` nor `internal/tenantsettings`, itself or through another package, and is in `fixedRuleGuards` (`tools/verify/compaction.go`).

## The test tenant

The test tenant has no route: its summaries are made by its recorded turns. `fixtures/test-tenant/turns/compaction.json` answers any request whose system text holds "running summary of a long chat", the shipped prompt's words, with one recorded summary. A model of another tenant that names the fake adapter gets the same answer.

## Settings

In `settings/chats.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `chats.compact` | true | tenant | Whether the oldest turns are summarised and the summary sent. Off, they are left out with the note, and no summary is sent. Pins work either way. |
| `chats.compact_trigger_percent` | 80 | tenant | Summarise past this share of the room for earlier turns. 1 to 100. |
| `chats.compact_keep_percent` | 50 | tenant | Summarise until the rest take no more than this. Less than the trigger. |
| `chats.compact_summary_max_tokens` | 1500 | tenant | The most a summary may take. Its share of a call may make it less. |
| `chats.compact_summary_heading` | one sentence | tenant | The line before the summary in a call. |
| `chats.compact_prompt` | see the file | install | The instruction to the summary model. `{words}` becomes the most words. |
| `chats.compact_message_max_chars` | 4000 | install | The most of one message the summary model is shown. |
| `chats.compact_calls_max` | 4 | install | The most calls one job makes. |
| `chats.compact_timeout` | 60s | install | How long a job may take. |
| `chats.compact_retry_after` | 10m | install | How long a chat whose summary failed waits before it is tried again. |
| `chats.context_pinned_heading` | one sentence | tenant | The line before a pinned message sent on its own. |

The share of a call the summary may take is the context builder's `chats.context_summary_percent`. The model is `routing.summary_model`.

## In the turn

Every existing line of `internal/turn` this item changed, all of them added lines:

- `context.go`, in `build`: `older := r.olderHistory(...)` before `contextbuild.Build`; `Summary: older.text, SummaryCovers: older.covers, Pinned: older.pins,` in its input; `r.compactLater(...)` after the record is stored.
- `route.go`, in `used`: a summary call, made with the step `stepSummary`, is recorded with the kind `summary`.
- `runner.go`: the field `compaction compactions` on `Runner`.
- `routes.go`: the three pin routes, after `GET /chats/{id}/model`.
- `context_test.go`: the long-chat test of P5-01 switches `chats.compact` off, since it is the test of the note.

New files: `compact.go`, `compact_test.go`, `pins.go`.

## Checked by

- `go test ./internal/compact`: when to summarise and which turns, in steps as a chat grows; the request holds the summary so far and the turns, writes tools out and leaves thinking out, takes fewer turns for a small window and cuts one too large; no room asks for nothing; the answer is read; summaries are kept, listed and go with their chat; `Usable` follows the mark.
- `go test ./internal/contextbuild`: a summary stands for the turns it covers and the call runs on from the first it does not; a summary too large for its share falls back to the note; shares that add up past the room never leave a turn counted as summarised; a pinned message is in every call once, in its turn or on its own, at every window; a pin in a summarised turn is sent; a pin larger than the window makes the call over.
- `go test ./internal/chat`: pins are set, listed and taken off, refused across chats and tenants, and go with their chat.
- `go test ./internal/turn`, through the routes with the fake provider:
  - a chat ten times its model's window, with two messages pinned through the routes, is answered six turns running; every call fits and holds both pins; from the second call on the recorded summary is sent in place of the oldest turns; the summaries form one chain, each standing for more than the one before; each summary call has a usage record of the kind `summary`;
  - a summary model that fails leaves the chat answered with the note, one line in the log, and one attempt in two turns;
  - a held chat is summarised by its local model and never by the hosted summary model it names; its summaries are sensitive; a call that may reach a model that may not read sensitive content is not given one.
- `verify`, `context fit` ([tools.md](tools.md)): for every fixture model, a chat grown one turn at a time to ten times its window, with two pins, summarised as the turn does by the fake provider behind the model's own guard: every call fits by the budget's check and ends with the newest message, every pinned message is in every call, each summary extends the one before, and the last call carries the summary in place of the oldest turns.

## Not here yet

- **Summaries in the turn inspector (P5-04).** The record says which turns the summary stood for (`Summarised`, and `Covers` on the summary part), and which pins were sent on their own. Nothing shows them yet, and no route returns a chat's summaries.
- **The pin in the chat screen.** The routes exist; the screen is the interface lane's. A pin is not yet on the `Message` shape.
- **Chat summaries for attaching and handing off (P5-05, P5-06).** These are summaries of the oldest turns of one branch, made for the window, not a summary of a whole chat. P5-05 can start from them.
- **Old rows.** Summaries are never removed, except with their chat.
- **A real summary model.** Only the fake has written a summary. How well a cheap model keeps detail over many extensions is unknown.
- **A restart forgets** which chats are being summarised and which failed. A job cut short by a restart is simply made again when needed.
