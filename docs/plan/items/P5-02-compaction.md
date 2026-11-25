# P5-02. Compaction

Status: doing (chats)
Lane: chats
Depends on: P5-01
Owner gate: none
Scope: Older turns summarised past a threshold; pinned items kept; summaries stored with the chat.
Proof: A chat ten times the window never exceeds it, and pinned items survive.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What changes:

- A new package, `internal/compact`. It decides when a chat's earlier turns are summarised and which, writes the summary request and reads the answer, keeps summaries in the tenant's database, and says whether a stored summary may go to a call. It reads no setting and opens no connection: the turn reads the settings and passes them in. It decides whether a summary of sensitive content may reach a model, so it is a guard of fixed rule 3.
- **When.** After each model call is built, the builder's record says how much room the earlier turns had and how much the turns that are not yet summarised take. When they take more than `chats.compact_trigger_percent` of it, or a turn was left out, the oldest are summarised until the rest take no more than `chats.compact_keep_percent`. So a summary is made in steps as the chat grows, never for every turn.
- **How.** The model routing gives the `summary` task (`routing.summary_model`, then the chat's own, then the fallback chain) is sent the summary so far and the turns that follow it, and asked for one summary that takes them in. A summary is extended, never made again from the start. The request is cut to what the summary model's window holds, and a job makes at most `chats.compact_calls_max` calls. The call goes through the route like any call: its audit entries, its usage record with the kind `summary`, and the spend cap.
- **Never in the way.** The summary is made beside the turn, in the background. The call it was started from is sent at once, with what was stored before. A failure, a timeout or a route with no model writes one log line and nothing else: the chat goes on as today, the oldest turns left out with the note, and summarising is tried again after `chats.compact_retry_after`.
- **Stored.** One migration, `20261125T110000_compaction.sql`: `chat_summaries`, one row for each summary with its chat, the first and last message it stands for, how many turns, the summary it extended, its text, the model that wrote it and whether it is sensitive; and `chat_pins`, the messages a person pinned. Both go with their chat.
- **Used.** Before each call the turn finds the newest summary whose last message is on the branch being sent, checks that it still stands for the start of that branch, and gives it to the context builder with how many messages it covers. The builder sends the summary in its slot in place of those turns. A summary that does not fit its share is not sent, and the covered turns are packed as before, with the note.
- **Sensitive.** A summary of a chat that was sent sensitive content (`recall.Held`) is made only by a model that may read it, because the summary route is routed as sensitive, and is stored as sensitive. A sensitive summary is given only to a call whose every model may read it.
- **Pinned.** `chat.Pin` and `chat.Pins` in the chat record, and three small routes: `GET /chats/{id}/pins`, `PUT` and `DELETE /chats/{id}/pins/{message}`. The context builder gains a `pinned` part: a pinned message whose turn is sent is there already, and one whose turn is left out or summarised is sent as a pinned item, which is never left out.
- **The builder.** `contextbuild.Input` gains `SummaryCovers` and `Pinned`; the record gains the room the earlier turns had, the room the summary may take, and which turns the summary stood for.
- **In `internal/turn`**: a new file, `compact.go`, with the glue. In `context.go`, `build` gains a few added lines: find the summary and pins before `Build`, pass them, and start the summary after it. In `route.go`, `used` gains the summary's kind. In `routes.go`, the three pin routes are added to the list. The `Runner` gains one field. No other existing line changes.
- **Settings** in `settings/chats.json`, added at the end.
- **The test tenant**: a recorded summary in its turns, matched by the summary prompt, so the fake provider writes summaries.

How it is proven:

- `go test ./internal/compact`: when to summarise and which turns, the request cut to the summary model's window, the answer read, the store, and the rule 3 check.
- `go test ./internal/contextbuild`: a summary stands for the turns it covers, a summary too large for its share falls back to the note, pinned messages are sent when their turns are not and never twice.
- `go test ./internal/turn`: a chat ten times its model's window, with two pinned messages, is answered turn after turn; every call fits, holds both pins, and after the first carries the summary in place of the oldest turns; the summary is extended, not made again; the summary calls have usage records of the kind `summary`; a failing summary model leaves the chat answered with the note and one log line; a held chat's summary goes to no hosted model.
- `verify`, `context fit`: a step that grows a chat to ten times each fixture model's window, summarising with the fake provider's recorded summary as the turn would, and proves every call fits and every pinned message is in every call.

### Left to do

Kept up to date at each commit, so another session can finish the item.

1. ~~The builder: `SummaryCovers`, `Pinned`, the `pinned` part, `TurnRoom`, `SummaryRoom`, `Summarised`, with tests (`internal/contextbuild/compaction_test.go`).~~
2. ~~`internal/compact`: options, `Plan`, the request and its cut to the summary model's window, reading the answer, `Save`/`List` over `chat_summaries`, `Usable` for rule 3; the migration with `chat_pins` too; `chat.Pin`/`chat.Pins`. A stub `docs/architecture/compaction.md`, its `componentDocs` line, and the guard's init in `tools/verify/compaction.go`.~~
3. ~~`internal/turn/compact.go`: find the summary and pins for a call, start the summary job beside the turn, the job itself; the added lines in `context.go`, `route.go` (`used`), `runner.go` (one field), `routes.go` (pin routes) and `internal/turn/pins.go`; `docs/api/openapi.json` regenerated.~~
4. ~~Settings at the end of `settings/chats.json`; the recorded summary in `fixtures/test-tenant/turns/compaction.json`.~~
5. ~~Tests in `internal/turn` (`compact_test.go`; `context_test.go` switches compaction off for its drop-with-note test), and the `context fit` step in `tools/verify/compaction.go`, called by added lines in `contextfit.go`.~~
6. Write-up: `docs/architecture/compaction.md` in full (now a stub), its overview row, a row in `internal/README.md`, lines in `context.md`, `turns.md`, `routing.md`, `chat.md`, `tools.md`; decisions from 0230; the journal `docs/journal/2026-11-25-chats-004.md`; the item's status and result.

