# Turn inspector

Folder: `internal/inspector`, joined to a turn and served by `internal/turn/inspect.go` and `internal/turn/inspectroutes.go`. The screen is `web/app/src/Inspector.tsx`, with `MessageActions.tsx`, `pins.ts` and `inspector.css`.

Shows what went into each turn, by section, with tokens and cost: every model call of the turn with the parts it was built from, its usage records, and the totals of the turn. From it a person can take a recalled page or source, or a pinned message, out of the chat's next turn, or take a pin off. A Pin action beside Copy on every message pins it.

The package reads no setting and opens no connection. It imports the context builder ([context.md](context.md)), usage ([usage.md](usage.md)), routing's kinds and the tenant handle. It enforces no fixed rule: taking an item out of a call only ever sends less, and never changes where a call may go (decision [0272](decisions/0272-a-removed-item-is-marked-and-recorded-and-never-moves-a-call.md)).

Decisions: [0270](decisions/0270-a-calls-usage-records-are-found-by-their-order.md) for joining usage records to calls, [0271](decisions/0271-a-skip-is-used-by-the-next-turn-and-no-other.md) for taking an item out of the next turn only, [0272](decisions/0272-a-removed-item-is-marked-and-recorded-and-never-moves-a-call.md) for how the context builder leaves it out, and [0273](decisions/0273-the-inspector-is-a-panel-opened-from-a-reply.md) for the screen.

## What one turn shows

A turn leaves three kinds of record, each kept by the part that writes it:

| Record | Kept by | Read with |
| --- | --- | --- |
| What each chat call was built from: every part with its tokens, every turn of the branch sent or left out | the turn, in `turn_contexts` ([context.md](context.md)) | `turn.Contexts` |
| One usage record for each model call: kind, model, outcome, tokens, cost, time | the turn, in `usage_records` ([usage.md](usage.md)) | `usage.List` with the turn's request id |
| What recall searched for, found, kept, dropped and withheld, and why | the turn, in `turn_recall` ([knowledge-recall.md](knowledge-recall.md)) | `recall.Saved` |

`inspector.Calls(contexts, records)` joins the first two. A usage record carries the request id of the API call that sent the turn, which every model call of the turn is made under (its chat calls, its title, the summaries made beside it), and no step. So a call's records are found by their order, kind by kind: a call's run of records ends at the one whose outcome is `ok` or `stopped`, or at the kind's last record, and the records before it in the run are the models of the route that did not answer. A turn whose chat call ends any other way ends there, so only its last chat call can, and the chat runs match the steps in order (decision [0270](decisions/0270-a-calls-usage-records-are-found-by-their-order.md)). A step whose call never ended, which the server stopping can leave, has its record and no usage; a usage record whose context was not kept still gets the next step.

The chat calls come first, by step, then every other call in the order it was made. Each `Call` has its records and their `Total`, and `Calls` also returns the totals of every record of the turn. The two always agree, because every record is in exactly one call.

## What a person takes out of the next turn

A **skip** is one item the chat's next turn leaves out: a page or source of the person's knowledge store that recall brought in, named by its id in the store, or one of the chat's pinned messages, named by its id. Skips are rows of `chat_skips` (migration `20261125T152500_chat_skips.sql`), and go with their chat.

| Function | Does |
| --- | --- |
| `Check(kind, ref)` | Refuses a kind that is not `page`, `source` or `message`, a message id that is not a whole number, and an id that is empty, longer than 200 characters, or holds a space or a control character. |
| `Add(ctx, s, chatID, skip)` | Keeps a skip for the chat's next turn. The same item twice keeps one. |
| `Remove(ctx, s, chatID, kind, ref)` | Takes back a skip that waits. A skip a turn has used stays. |
| `Waiting(ctx, s, chatID)` | The skips that wait for the chat's next turn. |
| `Claim(ctx, s, chatID, turnID)` | Gives the turn every waiting skip of its chat, and returns the turn's skips. |
| `OfTurn(ctx, s, turnID)` | The skips a turn claimed. |
| `LeaveOut(items, pins, skips)` | Marks the recalled items and pinned messages the skips name as removed, on copies. |
| `Ref(kind, id)` | The name an item goes by on the context builder's input and in its record: `page:7`, `source:3`, `message:42`. |

**Exactly once.** When a turn builds its first model call, `Runner.leaveOut` claims the chat's waiting skips for it; every later call of the turn reads the same ones with `OfTurn`, so the item stays out of the whole turn and does not come back between tool steps. The turn after finds none waiting. A turn that fails before it builds a call claims nothing, and the skips wait for the next one that does (decision [0271](decisions/0271-a-skip-is-used-by-the-next-turn-and-no-other.md)).

**In the call.** The turn gives each recalled item and each pinned message its `Ref`, and `LeaveOut` sets `Removed` on the ones a skip names. The context builder sends a removed item nowhere and holds it no room, and lists it in the record as removed with the tokens it would have taken. A removed pin whose turn is sent is in the call all the same, in its turn, and the record says so (decision [0272](decisions/0272-a-removed-item-is-marked-and-recorded-and-never-moves-a-call.md)).

**Where a call goes is not changed.** A turn's route is chosen before its calls are built, from what recall found, and the chat's sensitive mark is kept as recall found it. Taking a sensitive page out of a call leaves it out, and the call is still routed only to models that may read sensitive content.

**Pinning** is the chat record's (`chat.Pin`, with the pin routes of [compaction.md](compaction.md)).

## The routes

In the shell like every chat route ([turns.md](turns.md)), and in `docs/api/openapi.json`. `{id}` is the chat, `{turn}` the turn.

| Route | Reply |
| --- | --- |
| `GET /chats/{id}/turns/{turn}/inspect` | `InspectReply`, below. |
| `GET /chats/{id}/skips` | `SkipsReply`: `chat_id`, `skips`. |
| `PUT /chats/{id}/skips/{kind}/{ref}` | `SkipsReply`, after taking the item out of the next turn. No body. |
| `DELETE /chats/{id}/skips/{kind}/{ref}` | `SkipsReply`, after putting it back. |

**`InspectReply`**

| Field | Holds |
| --- | --- |
| `turn` | The `Turn`, as `GET /chats/{id}/turns/{turn}` gives it. |
| `calls` | Each model call, a `Call`: `step` (chat calls only), `kind`, `model` (the one the call ended on), `context` (the context builder's record: window, room, limit, tokens; every part with its state, tokens, kept, dropped and removed counts, and for recall and pinned messages each item with its label, `ref`, tokens and state; the summary's `ref`; every turn of the branch with its tokens and whether it was sent or summarised), `previews` (the first words of each turn's first message, in the order of `context.turns`), `usage` (its usage records) and `total`. |
| `recall` | The turn's recall record: the store, the query, how many hits were found, kept, dropped and withheld, and each hit with its type, id, space, title, score, whether it is sensitive, and why it was not kept. It names sensitive pages, so it is shown to the person and never sent to a model. Absent when the person has no store. |
| `summaries` | The summaries the calls were given, as stored with the chat ([compaction.md](compaction.md)): text, model, turns, whether sensitive. Read only. |
| `totals` | The sum of every usage record of the turn: calls, input, output, cache read, cache write and thinking tokens, cost in millionths of a US dollar, and whether a count was estimated. |
| `pinned` | The ids of the chat's pinned messages now. |
| `skips` | What the chat's next turn leaves out. |

Since P5-03 each call's `context` also has `order`, the parts sent in the order the request carried them, and `cache`, its cache points; the `recall` part's place is `messages`. The panel already shows each call's cache reads on its usage line and the turn's reads and writes under its tiles ([caching.md](caching.md)).

`PUT .../skips/...` refuses:

- a kind or an id `Check` refuses, and a message that is not pinned, with 400 `bad_request`;
- a message that is not in the chat, and a page or source that no call of the chat's newest fifty was given, with 404 `not_found`. The skip's label is the one that call's record gave the item.

A chat or turn that is not the person's is 404 `not_found` on every route. The route walks of `verify` call all four, since they take every route from `app.Routes` ([tools.md](tools.md)).

## The screen

Provisional: there is no inspector mockup (P1-06). Built from the tokens, after the inspector button in the header of `MainDark.html` (decision [0273](decisions/0273-the-inspector-is-a-panel-opened-from-a-reply.md)).

- **Beside Copy.** Every reply has Copy, **Pin** and **Inspect**; every message of the person's has Copy and **Pin** under its bubble, shown on hover and focus, always on a screen with no hover, and always once pinned. A reply's pin goes on its last message with text. A pinned message's Pin says "Pinned" in the copper ink. The pins are kept in `pins.ts`, by chat, outside the screens, read the first time a screen asks, and set again from the inspector's answer.
- **The panel.** Inspect opens a panel on the right, as wide as the screen on a phone. It reads `GET .../inspect`, and again when a turn of the chat ends. From the top: the turn's time, status and model; four tiles (tokens in, tokens out, cost, model calls) and a line for thinking, cache and estimates; what the next message leaves out, each with **Put back**; then each chat call: how full it was against its limit, as a bar and in words, each part that holds something with its state and tokens, and the parts that held nothing in one line. Earlier turns open to a list with their first words. Recalled items and pinned messages each say whether they were sent, left out for room or left out by the person, with their score and **Leave out next turn**, and a pinned message has **Unpin**. The summary opens to its text, read only. Each call's usage records close it. Then the turn's other calls (title, summary), and what recall found, with the reason for each hit it did not keep. Escape and the close button close it.
- Its calls, all in `src/api.ts`: `inspectTurn`, `listPins`, `setPin`, `listSkips`, `setSkip`.

Since P9-02 the reply has `uploads`: for each call whose branch held an image or a file, what it was given of each (sent as an image, sent whole as a file, its text with the attachments) and what was withheld and why (`vision`, `files`, `missing`), as `uploads.StepCall`. The screen lists the first call's in a section, Images and files, and the attachments part lists each file's text with its tokens ([uploads.md](uploads.md)).

## Checked by

- `go test ./internal/inspector`: records joined to steps through a fallback, a stopped call and summary calls made beside them; a last call that failed on every model, a call that never ended, a record with no context; the totals; skips kept once, claimed by one turn only, read by its later calls, taken back, refused across kinds and shapes, and gone with their chat; `LeaveOut` never crosses kinds and changes nothing it was given.
- `go test ./internal/contextbuild` (`removed_test.go`): a removed recalled item or pinned item is not sent, takes no room, and is listed as removed; a removed pin is held no room, and one whose turn is sent is recorded as in its turn; the summary part carries its `ref`; the record still rebuilds the request.
- `go test ./internal/turn` (`inspect_test.go`), through the routes:
  - the inspector's totals equal the sum of the turn's usage records, every record is in exactly one call and each call's total is its records', on a turn whose hosted model failed and a local fallback answered with a title call after it, on a paid turn whose totals carry its cost, and on the test tenant's task of three tool steps, where each step is a call with its own record;
  - a recalled page taken out through the routes is absent from the next turn's request in the audit log, recorded there as removed, and back in the request of the turn after; a page put back before the turn is sent;
  - a pinned message sent on its own, because its turn is left out of a chat twice the window, taken out the same way, is absent from the next request in the audit log and back in the one after;
  - an unpinned message, a message or page that is not there, a kind that is not one, and another person's session are refused.
- On November 25, 2026, in a browser against the binary built from this branch, on port 7961 with a data folder in the worktree, as the test tenant with two invented pages in its store: a turn of three tool steps inspected call by call; a reply pinned and shown as "Pinned"; a recalled page left out of the next message, the next turn's inspector showing it "left out by you" while recall kept it; a pinned message left out and put back; the panel in light and dark, and at phone width. No errors in the console.

## Not here yet

- **The mockup** (P1-06). Every word and layout choice of the panel and the actions waits on it.
- **The text of each part.** The panel shows labels and tokens; the text a call sent is in the audit log and is not read here.
- **Correcting a summary.** It is read only, as the owner question recommends for a first step.
- **Withheld items.** A hit withheld from a call for sensitive content is listed under what recall found; it cannot be brought in from the inspector, since only a local or trusted model may read it.
- **A sensitive item taken out still holds the chat.** The route and the chat's mark are decided from what recall found, before the person's choice is applied. Leaving that as it is keeps fixed rule 3's two guards as they were.
- **Pinning from the inspector.** Pins are made beside Copy; the inspector shows them and takes them off.
- **The header button.** `MainDark.html` puts an inspector button in the chat header; the panel opens from each reply instead, so it knows which turn to show.
