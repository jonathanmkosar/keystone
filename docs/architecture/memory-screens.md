# Memory screens and incognito

Files: `internal/knowledge/memory/forget.go` and `incognito.go`, `internal/knowledge/withdrawn.go`, `internal/knowledge/builtin/remove.go`, `internal/turn/memoryscreens.go` and `incognito.go`, `web/app/src` (`Memory.tsx`, `Incognito.tsx`, `memoryCalls.ts`, `memory.css`), `tools/verify/memoryscreens.go`

A person can see what their knowledge store remembers, correct a fact and forget one, and start a chat that keeps no record. This is P6-07. The screens are provisional until the memory mockups (P1-06) are approved; the server side is complete.

Decisions: [0370](decisions/0370-forgetting-a-fact-replaces-its-source.md) for what forgetting and correcting do, [0371](decisions/0371-with-waymarks-a-forgotten-source-is-withdrawn.md) for Waymarks, and [0372](decisions/0372-an-incognito-chat-is-deleted-when-left-or-idle.md) for incognito chats. The memory pass, whose sources these are, is in [memory-pass.md](memory-pass.md), and the store interface in [knowledge.md](knowledge.md).

## What is remembered

The Memory screen lists what the memory pass added, from its own ledger, `memory_passes`: each pass that ended `done` with a source, the newest first, at most `knowledge.memory_screen_memories`. Nothing else in the store can be corrected or forgotten here, which keeps "forget" to Keystone's own intake and to the person's own database (fixed rule 1).

`GET /memory` answers `MemoryOverview`:

| Field | Holds |
| --- | --- |
| `store` | The adapter, `can_remove` (the store has `RemoveSource`), `can_add` (it has `AddSource`), a note for a person saying what forgetting does in this store, and `unavailable` when the store did not open or its catalog could not be read. |
| `profile` | The profile as the person wrote it. Edited on Settings → Profile. |
| `map` | The map of the store, the whole of it: the person's own, shown to them and sent to no model here. |
| `spaces` | Each space, by name: whether it is sensitive (its catalog label, or the ledger's mark for a space with no page), the sources the memory pass added to it with their facts, the chat each came from (its title, or that it was deleted) and the day, and its pages, at most `knowledge.memory_screen_pages`, with the rest counted. |
| `more` | The older sources past the limit, not listed. |

## Correct and forget

`POST /memory/forget` with `{"source_id", "fact"}`, and `POST /memory/correct` with `{"source_id", "fact", "text"}`. Both answer `MemoryChangeReply`: the change (the source, whether the store removed it, the new source with the facts that are kept, the space and its label), whether the profile followed, and a note for a person.

`memory.Apply` does the work, over the store interface alone (decision 0370):

1. The ledger names the source, or it is `not_found`. The fact must be one of its facts, or it is `not_found`.
2. A correction is checked: some words, at most `knowledge.memory_fact_max_chars`, spaces made single.
3. The old source is read. One the store no longer holds lets the ledger go (`ErrGone`, `not_found`).
4. The facts that are kept, the corrected one among them, go back as one new source with the old one's space, name, origin and date. A sensitive source's facts must land sensitive, or the new source is taken back and the change refused (`landed`, 409).
5. The old source goes: removed when the store has `RemoveSource`, withdrawn when it does not.
6. The ledger row points at the new source, holds the facts that are kept, and says why (`ReasonForgotten`, `ReasonCorrected`). No word of the forgotten fact is kept.

Then the profile follows: a line that says the fact, list mark, letter case, spaces and a closing full stop aside, is taken out or corrected (`memory.ProfileEdited`), within `knowledge.profile_max_chars`.

The person map lists pages, and a source is never in it, so a forgotten fact was never there. It is rebuilt as it always is, when the catalog changes.

| Answer | When |
| --- | --- |
| 200 | Done. |
| 400 `bad_request` | No source or fact; forget with text; correct without text; a correction too long or blank. |
| 404 `not_found` | The source is not one the memory pass added for this person, or does not hold the fact. Another person's source is this too. |
| 409 `no_store` | The person has no store. |
| 409 `cannot_change` | The store takes no new source, so the facts that are kept cannot go back. |
| 409 `landed` | The store put a sensitive source's facts in an ordinary space; taken back. |
| 503 `unavailable` | The store did not open or could not be reached. Nothing changed. |

## Removing and withdrawing

`knowledge.Capabilities` has a fifth flag, `RemoveSource`, and `knowledge.Store` the call `RemoveSource(ctx, id)`: the source can no longer be read or found, and a page that cited it no longer does and moves on a revision. The built-in store has it (`builtin/remove.go`): it deletes with secure delete on and optimises the source index. `none` and Waymarks answer `ErrNotSupported`. The contract suite's `remove source` check proves it, and fails a store that still finds a removed source or leaves a page citing it.

For a store without it, Keystone withdraws the source (`knowledge.Withdraw`, `knowledge_withdrawn`, decision 0371). `knowledge.Open` hands back, for a tenant and any adapter but `none`, the adapter's store with the tenant's withdrawn ids left out of search hits, reads (`ErrNotFound`) and a page's citations. `knowledge.Unwrap` gives an adapter's tests their own type back. A list that cannot be read makes the call `ErrUnavailable`: a chat carries on with no store rather than with a source it was asked to forget.

## Incognito

An incognito chat keeps no record (decision 0372):

| | |
| --- | --- |
| Made | `POST /chats/incognito`, incognito before its first message; the switch under the new-chat composer. |
| Memory | No memory pass (P6-06). |
| Recaps | None: `recapLater` skips it and `makeRecap` refuses it (`POST /chats/{id}/recap` answers 409 `incognito`). |
| Past-chat search | `search_chats` and `read_chat` leave it out from every other chat. |
| Left | `POST /chats/{id}/close`, which the chat screen sends when it goes, and with the browser's beacon when the page goes. The chat is deleted with everything that refers to it, and so are the chats of the tasks it started. A turn still running is stopped. A chat that is not incognito is refused with 409 `not_incognito`. |
| Idle | The sweep (`Runner.SweepIncognito`, started by `app.Handler`) deletes, at start and every `knowledge.incognito_sweep_every`, the chats that were left and those idle longer than the person's `knowledge.incognito_keep`. |
| Tasks | A chat a background task still works in, or one an unfinished task was started from, waits for the task to end, then goes. |
| Stays | Its calls in the audit log (fixed rule 4) and its usage rows, which hold tokens and cost and no words. |

`memory.Leave` marks a chat left (`memory_chats.left_at`), and `memory.IncognitoChats` lists them for the sweep. `SetIncognito` clears the mark.

## The screens

Provisional on the memory mockups; built from the tokens alone.

- **Memory** at `/memory`, in the sidebar after Scheduled: the store's note; each space as a card with its facts grouped by the reply they came from, a link to the chat, Correct (an inline box) and Forget (asks first), and its pages folded away; a sensitive space's facts hidden until Show. Beside them the profile, with a link to edit it, and the map. After a change the server's note is shown and the screen reads again.
- **Incognito**: a switch under the new-chat composer, and on an incognito chat a dashed copper mark at the top saying nothing is remembered and it is deleted on leaving. Leaving the chat's screen closes it a moment later; coming straight back, as React's strict mode does, cancels the close.

On November 25, 2026, on a server of this branch with the test tenant and the built-in store, the Memory screen listed the beehives facts with their chat, Forget and Correct worked, and an incognito chat was deleted when the person left it for a new chat; light and dark, and a phone's width, looked right.

## Settings

In `settings/knowledge.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `knowledge.incognito_keep` | 24h | tenant | How long an incognito chat that was not left is kept once nothing more is said in it, from 5m to 720h. |
| `knowledge.incognito_sweep_every` | 1m | install | How often the sweep looks, from 5s to 1h. |
| `knowledge.memory_screen_memories` | 200 | tenant | The most sources the Memory screen lists, from 1 to 5000. |
| `knowledge.memory_screen_pages` | 50 | tenant | The most pages of each space it lists, from 0 to 1000. |

## Fixed rules

- **Rule 1.** Every route works on the session's own tenant. A change goes through that tenant's ledger, so another person's source is `not_found`; another person's incognito chat is `not_found` to close. The route walks of `tenant isolation` cover the five new routes.
- **Rule 3.** A sensitive source's kept facts stay in its sensitive space, or the change is taken back. No model is asked anything by these routes. `internal/knowledge/memory` stays a guard that imports no setting.
- **Rule 4.** Incognito ends at the audit log, which keeps every call. The API wrapper records the routes without bodies, so a forgotten fact's words are not written there by forgetting it.

## Shared code changed

Every existing line this item changed outside its new files:

- `internal/knowledge/knowledge.go`: the `RemoveSource` flag in `Capabilities` and the `RemoveSource` call in `Store`, each with its comment (added lines).
- `internal/knowledge/registry.go`: in `Open`, five lines before the last `return s, nil` that hand back the withdrawing store.
- `internal/knowledge/none.go`, `internal/knowledge/waymarks/waymarks.go`: a `RemoveSource` method each (added lines).
- `internal/knowledge/builtin/builtin.go`: `Capabilities` gains `RemoveSource: true` (one line changed).
- `internal/knowledge/memory/passes.go`: `SetIncognito`'s upsert also sets `left_at = ''` (one line changed).
- `internal/knowledge/knowledgetest`: the `remove source` check, a line in `checkUnsupported` and one in `checkCancelled`; the test store's `RemoveSource` and two faults.
- Tests: `builtin_test.go` and `waymarks_test.go` reach their own type through `knowledge.Unwrap` (one line each); `builtin_test.go`'s capabilities line; `memory_test.go`'s `newTurn` numbers its turns, because the clock alone gave two turns one id.
- `internal/tools/builtin/pastchats.go`: an import, four lines in `search_chats`'s loop, and three in `read_chat`.
- `internal/turn/recap.go`: three lines in `recapLater` after the recap setting is read, and three in `makeRecap` after the chat is read.
- `internal/app/app.go`: four lines after the memory routes (a comment and `MemoryScreenRoutes`, `IncognitoRoutes`), and three after `Recover` that start the sweep.
- `tools/verify/main.go`: `{"memory screens", checkMemoryScreens}` at the end of `checks`, and the `componentDocs` lines for this page.
- `web/app/src/api.ts`: `call` is exported. `App.tsx`: an import and the `/memory` route. `Shell.tsx`: the Memory place. `Icon.tsx`: the `memory` and `incognito` shapes. `Chat.tsx`: two imports, the switch's state and line, `createIncognitoChat` when it is on, and the mark at the top of a chat.
- `settings/knowledge.json`: four entries after `knowledge.memory_timeout`.
- `docs/api/openapi.json`: regenerated for the five routes.

The migration is `20261125T193700_memory_screens.sql`: `knowledge_withdrawn`, and `left_at` on `memory_chats`.

## Checked by

`go test ./internal/knowledge/...`: the contract suite's `remove source` check passes the built-in store and the test store with the flag, and catches a store that still finds a removed source and one that leaves a page citing it; a withdrawn source is not read, found or cited through `Open`, the store keeps it, and another tenant's withdrawal touches nothing; a forgotten fact is gone from the built-in store and from search, the other fact kept in a new source the ledger follows, and the last fact forgotten leaves no source and no remembered row; a correction replaces the fact; an unknown source, another tenant's, a fact the source lacks and a bad correction are refused; a sensitive source's facts stay sensitive and a store that lands them in an ordinary space has them taken back; a store without `RemoveSource` has the source withdrawn, and one without `AddSource` refuses a change that keeps facts; the profile follows. With the fake Waymarks: forgetting withdraws the source, adds the kept fact under the intake token, and the instance still holds the old source.

`go test ./internal/tools/builtin`: an incognito chat is found and read from no other chat, and from inside itself it reads.

`go test ./internal/turn`, through the routes, in the test tenant with the built-in store: the Memory screen lists the beehives facts with their chat and a sensitive space's pages; a probe turn recalls the source before and not after it is forgotten; another person's forget is `not_found`; the profile line goes; the correction and every refusal answer as above; with no store the note says so. An incognito chat queues no pass, is refused a recap, cannot be closed by another person, and once closed leaves no message, turn, event, pass, recap or search row, while its call stays in the audit log and its usage row; an ordinary chat cannot be closed. The sweep keeps chats that are neither left nor idle, deletes the idle one a day on and the left one once its task ends, and follows `knowledge.incognito_keep`.

`verify`, `memory screens` ([tools.md](tools.md)), with the real server on a throwaway folder, the test tenant and the knowledge fixture in its built-in store: the memory pass's fact is on the Memory screen and recalled; another person cannot forget it; forgotten, its source is gone from the built-in store itself, no source the store finds holds it, recall no longer finds it, and the other fact is kept; an incognito chat queues no pass and puts nothing in the store, is refused a recap, is left out of `search_chats` from another chat while it is open, and once left leaves no message, turn, event, recap, summary or search row, while its call stays in the audit log. Faults put in by hand were each caught: the incognito chat searched, the source withdrawn and not removed, and the recap made.

## Not here yet

- **Forgetting a page.** The interface has no page writes until the wiki upkeep jobs (P8-06); pages are listed and not changed.
- **A Waymarks issue or notice for a withdrawn source**, so its own agents know (an owner question; decision 0371).
- **The facts of a deleted chat** stay until forgotten; the screen says the chat was deleted.
- **Approved screens.** Both are provisional on the memory mockups.
