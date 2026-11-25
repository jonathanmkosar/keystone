# P5-04. Turn inspector

Status: doing (chats)
Lane: chats + interface
Depends on: P5-01
Owner gate: Approval of the inspector mockup (P1-06), for the screen only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Per-turn view of sections, tokens and cost; pin and remove for the next turn.
Proof: The inspector's totals equal the usage record; a removed item is absent from the next call.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What changes:

- **A new package, `internal/inspector`.** Plain functions and data, no setting, no connection:
  - `Calls(contexts, records)` puts a turn's model calls together: each chat call (its step) with the context builder's record of what it was built from and its usage records, then the turn's other calls (title, summary, any other kind) with theirs, and the totals of every usage record of the turn. The usage records carry no step, so a step's records are found by their order: a step's run of `chat` records ends at the one that is `ok` or `stopped`, or at the last; the records before it are the models of the route that did not answer. A decision record says why that rule holds.
  - Skips: what a person took out of a chat's next turn from the inspector. One recalled item (a page or a source, by its id in the knowledge store) or one pinned message. Rows of a new table, `chat_skips` (one migration), with `Add`, `Remove`, `Waiting`, `Claim` and `OfTurn`. A turn claims every waiting skip of its chat when its first call is built, and every call of that turn honours them; the turn after does not. So a skip is used by exactly one turn.
  - `LeaveOut(items, pins, skips)` marks the matching recall items and pinned messages as removed.
- **The context builder.** `Item` and `ItemRecord` gain `Ref` (the inspector's name for an item: `page:7`, `source:3`, `message:42`) and `Removed`. A removed item is not sent, takes no room, and is listed in the record as removed with the tokens it would have taken; a removed pin is not sent on its own. `PartRecord` gains `Removed` (a count) and `Ref`, which names the summary a call was given (`Input.SummaryRef`). Nothing else changes in how a call is packed.
- **In `internal/turn`**, new files `inspect.go` (the glue in `build`, the route handlers) and `inspectroutes.go` (`InspectorRoutes`), and a few changed lines, each listed in the journal: in `context.go`, `build` applies the skips and names the summary; in `recall.go`, `recallFor` gives each recalled item its `Ref`; in `compact.go`, `olderHistory` gives each pin its `Ref`. `internal/app` adds `InspectorRoutes` with one line.
- **Routes**, in the shell like every chat route:
  - `GET /chats/{id}/turns/{turn}/inspect`: for one turn, each model call with its parts (rules, tools, profile, person map, current, recall items, summary, pinned, recent turns kept and dropped, note) and their tokens, the first words of each turn, the call's usage records and their total; the turn's recall record (each hit with its source, score and why it was kept or not); the summaries the calls were given, read only; the totals of every usage record of the turn; the chat's pins and waiting skips.
  - `GET /chats/{id}/skips`, `PUT` and `DELETE /chats/{id}/skips/{kind}/{ref}`: what the next turn leaves out. A page or source must have been recalled in the chat; a message must be pinned.
  - Pinning uses the existing pin routes.
- **The screen**, in new files: `Inspector.tsx` and `inspector.css` (the panel, opened from a reply), `pins.ts` (a chat's pins, kept outside the screens), `MessageActions.tsx` (Pin and Inspect beside Copy). Added lines in `api.ts` (the calls) and `Icon.tsx` (a pin and a layers icon); a few changed lines in `layout.ts` (a reply knows its turn and the message a pin goes on, a person's message its id), `Conversation.tsx` (the actions) and `Chat.tsx` (one line draws the panel). Built from the tokens, after the inspector button in `MainDark.html`'s header; there is no inspector mockup, so the screen is provisional.
- `docs/api/openapi.json` regenerated. The route walks in `verify` take every route from `app.Routes`, so the new routes are walked by `tenant isolation`, `calls recorded` and `no keys in output` with no new code.

How it is proven:

- `go test ./internal/inspector`: grouping records into calls, with a fallback, a stopped call, a failed last call, title and summary calls and a call with no record; the totals; skips added once, claimed by one turn only, removed, refused across chats; `LeaveOut`.
- `go test ./internal/contextbuild`: a removed recall item and a removed pin are not sent, take no room and are listed as removed; the record still rebuilds the request.
- `go test ./internal/turn`, through the routes: the inspector's totals equal the sum of the turn's usage records, call by call and in all, on a turn with a fallback, on a turn of several tool steps and on the test tenant; a recalled page and a pinned message sent on its own, each removed through the routes, are absent from the next call's request in the audit log and back in the call after; the routes refuse another person's chat.

Gated: the screen waits on the inspector mockup (P1-06). The server side is not gated.

### Left to do

Kept up to date at each commit, so another session can finish the item.

1. ~~The builder's `Ref`, `Removed` and `SummaryRef`, with tests.~~
2. ~~`internal/inspector`: calls and totals, skips and the migration, `LeaveOut`, with tests; its `componentDocs` line and a stub doc.~~
3. ~~`internal/turn`: the glue in `build`, the routes, `InspectorRoutes` in `internal/app`, tests; `docs/api/openapi.json`.~~
4. ~~The screen: pins and the inspector panel, `web/dist/` rebuilt, seen in a browser on port 7961.~~
5. Write-up: `docs/architecture/inspector.md`, the overview row, lines in `context.md`, `turns.md`, `compaction.md`, `knowledge-recall.md`, `web.md`; decisions from 0270; the journal `docs/journal/2026-11-25-chats-005.md`; the item's status and result.
