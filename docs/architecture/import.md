# Import from New Keystone

Folders: `internal/importer` (reads New Keystone's database and writes a tenant), `internal/imported` (the record of what came), `internal/importer/importertest` (generated sources and the proof), the command in `tools/tenant/importcmd.go`, `internal/chat/imported.go` (a chat written with its own times), `internal/turn/imported.go` (the mark in the API) and `web/app/src/Imported.tsx` (the mark on the screen)

New Keystone is the owner's earlier Python app. The import brings one person's chats and routines from a copy of its database into one tenant: the chats open and are found by search like any other, and each routine is a paused schedule, the morning brief among them. It is run from the command line, by whoever has a shell on the machine, and is the plan's cutover step "import of old chats and routines" (P12-03).

Built in P12-03. Decisions [0510](decisions/0510-the-continuing-chat-is-cut-into-chats-at-its-gaps.md) to [0515](decisions/0515-the-import-reads-a-whole-copy-and-prints-no-words.md) say why it is built this way. The steps for the owner are in [the runbook](../runbooks/import-new-keystone.md).

## What New Keystone keeps

Read from its source (`keystone/store.py`, `routines.py`, `runtime.py`), never from anyone's data:

- **One file, `workspace.sqlite`, for every workspace**, in write-ahead mode. Every table has a `workspace` column; a workspace is one person.
- **No chats.** A workspace is one continuing conversation. Each turn is a row in `jobs`: the person's prompt, the reply, a status (`queued`, `running`, `stopping`, `waiting_decision`, `succeeded`, `failed`, `cancelled`), a kind (`chat` or `build`) and the model the person picked, if they picked one. `messages` holds at most one `user` and one `assistant` row for each job, with their times, and a reply's web sources as a JSON list.
- **Events, not steps.** `events` says what happened in a turn as short, safe words ("Tavily basic · 1 credit · 3 sources · 420 ms."). It never holds what a tool was asked or what it answered. The model that answered is named in the `model.started` and `model.completed` events.
- **Routines**: a saved prompt with a cadence (`hourly`, `daily`, `weekdays`, `weekly` on one day, `once` on a date, or `manual`), a status (`pending`, `active`, `paused`, `deleted`), and times in one fixed zone, America/Chicago. The morning brief is the built-in one, whose id is the first 32 hex characters of the SHA-256 of `keystone:morning-brief`. `routine_runs` ties a run to its job.
- **Artifacts are built apps**: a digest, a summary, a state and the names of the files. The files are on disk, not in the database.
- **Nothing is marked sensitive.** There is no such column or table.

## What becomes of each thing

| In New Keystone | Here |
| --- | --- |
| A turn a routine started | A chat of its own, titled by the routine's name and the date in New Keystone's zone ("Morning brief, Wed 11 Nov 2026"), as a scheduled run's chat is titled here. |
| A build | A chat of its own. The app it made is named in the chat's note: its summary, its state and how many files. The files are not brought. |
| Every other turn | A message and its reply in a chat. The continuing conversation is cut into chats wherever more than `chats.import_chat_gap` passed between one turn and the next (decision [0510](decisions/0510-the-continuing-chat-is-cut-into-chats-at-its-gaps.md)). A chat's title is the first words of its first message, cut at a word's end to `chats.import_title_max_chars`. |
| A message | A text block, with its role and its original time. A reply's web sources are a "Sources:" list at its end, where New Keystone showed them, so the numbers it cites still lead somewhere; only an http or https address becomes a link. A text file sent with a message is a second text block, "Attached file: name", then its text. |
| The model named for a reply | Kept with the message, and given as the message's `model` by the API. It is New Keystone's name for it, which need not be a name in the person's registry here. |
| A turn that failed or was cancelled | The person's message, with no reply. The chat's mark counts them. |
| A turn still `queued`, `running` or `stopping` when the copy was made | Left for a later run. |
| Events | Counted by kind and summarised in the chat's note. They are not recorded steps (decision [0511](decisions/0511-new-keystones-events-are-counted-in-a-note-not-recorded-as-steps.md)). |
| A routine that is `active` or `paused`, hourly, daily, on weekdays, weekly, or once at a time still to come | A schedule with its name, its prompt and its times, **paused**. The person switches it on. |
| A `manual` routine, one only proposed (`pending`), a `deleted` one, a one-time one whose time has passed | Left out, and counted by reason. |
| Notebook entries (`memories`), decisions on builds, calendar and email drafts, build proposals, reports on built apps, trial records, New Keystone's own wiki tables | Left out, and counted (decision [0514](decisions/0514-memories-drafts-and-built-apps-are-left-in-new-keystone.md)). |

Nothing is rewritten on the way: a message's words are the source's, and the import adds only the "Sources:" list and the "Attached file:" line.

## An imported chat

- **It is an ordinary chat.** It is written by `chat.Import`, as one branch, with the chat made at its first message's time and last active at its newest, so it takes its true place in the list. Its title and messages are in the search tables in the same transaction, so the chat search and the model's `search_chats` find it at once. It can be renamed, starred, continued, attached, shared and deleted.
- **No turn wrote it.** It has no rows in `turns`, so its messages have no turn id, usage or cost, the inspector has nothing to show for them, and Operations lists nothing for it (decision [0512](decisions/0512-an-imported-chat-has-messages-and-no-turns.md)).
- **It wears a mark**: a row in `imported_chats`, given as `imported` on the chat by `GET /chats`, `GET /chats/{id}` and `PATCH /chats/{id}`. The mark says where it came from, when, what it was there (`chat`, `routine` or `build`), how many turns it held and how many got no reply, the times of its first and last message, the models named, the events counted by New Keystone's own event type, and a note in words: what New Keystone recorded that the chat record cannot hold. The screen shows a small "imported" mark on the chat's row in the sidebar and a line at the top of the chat.
- **Its recap** is made as an older chat's is: at the end of its next turn, or when `POST /chats/{id}/recap` is asked. Until then the past-chat search finds it by its title and its messages. There is no sweep ([chat-summaries.md](chat-summaries.md)).
- **Continuing it** works like any chat. The next turn's model is the person's default: New Keystone's model name is not a turn's, so it is not taken as the chat's model.

## Fixed rules

- **Rule 1, tenant isolation.** A run is given one tenant's store and writes there and nowhere else: the chats, the schedules, the record and the audit entries are all in that tenant's database. A source that holds several workspaces gives up one, the one named. The proof reads another tenant's tables and session after an import and finds nothing.
- **Rule 3, sensitive content.** New Keystone marked nothing as sensitive, read one Waymarks space, and sent every chat to whichever model it was set to, hosted ones included. So imported chats are **not held** by default (decision [0513](decisions/0513-imported-chats-are-not-held-unless-the-admin-says-so.md)). The admin can say otherwise before the import with `chats.import_hold`: `wiki` holds the chats in which New Keystone read the person's wiki (pages were recalled into a call, or the model looked one up), and `all` holds every imported chat. A held chat is held as any is: a row in `turn_recall`, written in the transaction that writes the chat, on a turn of the chat that calls no model (actor `import`). From then on `recall.Held` is true for it, its recap is written only by a local or trusted model, and the past-chat search leaves it out for a hosted one.
- **Rule 4, every call recorded.** The import calls no model and no tool of the agent's. It is itself in the tenant's audit log: a `started` entry before anything is written and an `ok` or `error` entry after, of the kind `tool`, the action `import_new_keystone` and the actor `admin`, under the request id `import-<run id>`. The entries hold counts, the settings used and the copy's SHA-256, and no word of a message.
- **Rule 2** is not touched: nothing outside Keystone is changed. Every schedule arrives paused, and a run of one, once switched on, asks for approval like any other.

## Twice is once

`import_sources` holds every source id that was brought, keyed by source, workspace, kind (`turn` or `routine`) and id. It is read before anything is planned, and what is there is skipped. Each chat is written in one transaction with its mark, its messages' rows and its turns' source ids, so a run that is cut short leaves whole chats, and the next run goes on after them.

- A second run of the same copy brings nothing.
- A later copy brings the turns that are new, in chats of their own; they are not added to a chat an earlier run made.
- A source id is never removed. A chat or a schedule the person deletes does not come back.
- A schedule is made by `schedules.Scheduler.Create` and noted straight after. Should a run stop between the two, the next run finds a paused schedule with exactly that name, prompt and times that no routine is noted for, and notes it instead of making a second.

## The source

`importer.OpenSource` opens the file read-only and as a file nothing else writes (`mode=ro&immutable=1`), so nothing is written to it or beside it. Before that it refuses:

- a file with a write-ahead log or a journal beside it that holds anything: that is the live database, or a plain copy of it, and its newest turns may be missing or torn;
- a file SQLite's quick check does not pass;
- a file without the columns `jobs` and `messages` have had since New Keystone's first build.

Columns and tables later builds added are read when they are there and taken as empty when not, so a database only an old build opened is read too.

`importer.Copy` (the command `import-copy`) makes the whole copy: it opens the database read-only and runs `VACUUM INTO`, which writes one file holding everything the database and its log hold at one moment, then reads the copy back. It works while New Keystone is running. Reading a database in write-ahead mode makes SQLite keep its shared-memory file beside it, as any reader does; nothing else is written there.

## The command

    go run ./tools/tenant import-copy <workspace.sqlite> <new file>
    go run ./tools/tenant import --check [--workspace <name>] <tenant-id> <copy>
    go run ./tools/tenant import [--workspace <name>] <tenant-id> <copy>

- `--check` is the dry run: `importer.Check` reads the copy and the tenant's record of earlier imports and prints what a run would bring. It writes nothing, not even an audit entry.
- Without it, `importer.Run` brings the workspace in and prints what it brought.
- `--workspace` names the workspace when the copy holds several. With one, it is not needed.
- **Neither prints a word of the source**: no title, no routine's name, no message. They print counts, the names of models and of tables, dates, and the first characters of the copy's SHA-256. The output can be shown to anyone, and a session that runs the import for the owner reads nothing of his (decision [0515](decisions/0515-the-import-reads-a-whole-copy-and-prints-no-words.md)).

The report: how many turns and routines the copy holds; what earlier runs brought; chats by kind, turns, messages, the period, held chats, schedules and whether the brief is among them; replies by the model named; what happened to the person's time zone; what was left out, by reason; the copy's other tables by rows with what becomes of each; notebook entries by state.

## The time zone

New Keystone kept every routine's time in America/Chicago, which is `chats.import_time_zone`. A schedule here runs at wall-clock times in the person's own zone, `agents.time_zone` ([schedules.md](schedules.md)). When a run brings a schedule:

- a person with no zone of their own is given New Keystone's, so 07:00 there is 07:00 here, and the report says so;
- a person with a zone of their own keeps it, the schedules keep their times on the clock, and the report says the two differ.

A routine run's chat is dated in New Keystone's zone either way.

## Stored

Migration `20261126T022700_imports.sql`. `internal/imported` owns the four tables.

| Table | Holds |
| --- | --- |
| `imports` | One row for each run that wrote something: its id, the source and workspace, the copy's SHA-256, the request id of its audit entries, counts of chats, messages and schedules, and when it started and ended. An empty end is a run that was cut short. |
| `import_sources` | One row for each turn or routine brought: where it came from and what it became (`chat_id` or `schedule_id`). Never removed. Ids only. |
| `imported_chats` | A chat's mark, keyed by the chat, which it goes with. |
| `imported_messages` | For an imported message, the source's turn and the model named for a reply. It goes with its message. |

With `chats.import_hold`, a held chat also has one row in `turns` and one in `turn_recall`, as above.

| Function | Does |
| --- | --- |
| `importer.OpenSource`, `Copy` | Open a whole copy; make one. |
| `Source.Settle`, `Source.Read` | Which workspace; everything of it the import reads, as plain values. |
| `importer.MakePlan` | What a workspace becomes: chats, schedules and what is left, from what was read. It writes nothing, so the dry run and the import see the same thing. |
| `importer.Check`, `Run` | The dry run; the import. |
| `Report.Write` | The report, in words and counts. |
| `chat.Import` | Writes a whole chat with the times given, and runs the caller's function in the same transaction. |
| `imported.Seen`, `Note`, `MarkChat`, `MarkMessage`, `Marks`, `Models`, `Begin`, `End`, `Count` | The record. |

## Settings

In `settings/chats.json`. Each is the admin's to set for one person, before the import: `go run ./tools/tenant setting set <tenant-id> <key> <value>`.

| Key | Default | Meaning |
| --- | --- | --- |
| `chats.import_chat_gap` | 1h | The continuing chat is cut where more than this passed between turns, from 1m to 720h. |
| `chats.import_title_max_chars` | 60 | The most characters of a first message a title takes, from 10 to 150. |
| `chats.import_time_zone` | America/Chicago | The zone New Keystone kept its routines' times in. |
| `chats.import_hold` | none | Which imported chats are held to local or trusted models: `none`, `wiki` or `all`. |

It also reads `chats.title_max_length`, `agents.schedule_max` and `agents.schedule_prompt_max_chars`, which a schedule and a title must fit. A routine past the most schedules a person may have is left out and counted.

## In shared code

Every existing line this item changed or added to in shared files:

- `internal/turn/routes.go`: the import of `internal/imported`, and the field `Imported` on `Chat`, after `Summary` (added lines).
- `internal/turn/handlers.go`: in `whole`, `r.withImport(ctx, s, &out)` before the return; in `list`, `r.withImports(...)` after `withSummaries` (added lines); in `patch`, the last line `api.WriteJSON(w, http.StatusOK, shown(got, busy[id]))` became three, so a renamed or starred chat keeps its mark.
- `tools/tenant/main.go`: the commands in the package comment, one usage line, and the cases `import` and `import-copy` (added lines).
- `tools/verify/main.go`: `{"import", checkImport},` after the share check (an added line). The `componentDocs` lines and the network line are in an `init` in the new `tools/verify/importcheck.go`.
- `settings/chats.json`: the four settings above, before `chats.context_pinned_heading` (added lines).
- `docs/api/openapi.json`: regenerated, for the field on `Chat`.
- `web/app/src/api.ts`: the `imported` field on `Chat` and its type; `Shell.tsx` and `Chat.tsx`: an import and one line each.

New files: `internal/importer/*`, `internal/imported/*`, `internal/chat/imported.go` and its test, `internal/turn/imported.go`, `tools/tenant/importcmd.go` and its test, `tools/verify/importcheck.go`, the migration, `web/app/src/Imported.tsx` and `imported.css`.

## Checked by

Every source in the tests is generated in New Keystone's schema by `importertest.Build`, with an invented person and invented words. No test, and no step of this item's work, opened a real New Keystone database.

- `go test ./internal/importer`:
  - **the item's proof** (`importertest.Proof`, on the real server with the test tenant): the dry run writes nothing and counts what the import then brings; the imported chats are in the list with their mark and the chats that were there have none; a chat opens with its messages in order, their roles, their original times and the models named, its web source and its attached file's text; the chat search and `search_chats` find it, and `search_chats` in another tenant's store does not; the morning brief is a paused schedule, daily at 07:00 in America/Chicago, that does not run while a morning passes, and once switched on is due the next morning and runs then with the fake provider, in a chat titled by its name and date that holds the prompt and the recorded reply; a second import brings nothing; the import made no model call and has no usage record, and is in the audit log as a started and an ok entry that hold no word of the source; another tenant's tables, audit log and session hold and reach none of it;
  - the standard fixture becomes five chats and three paused schedules, each checked against the fixture: titles, times, roles, blocks, models, marks, notes, the counts of everything left out and of every other table, and no turn written; a deleted chat stays deleted after a second run;
  - the dry run writes no row in any table, and neither report prints a prompt, a reply, a title, a routine's name or the workspace's name;
  - a later copy brings only the turn that has ended since and the new one, in chats of their own;
  - `chats.import_hold` as `wiki` holds the one chat New Keystone read the wiki in, and as `all` every chat; each held chat is sensitive to `recap.Sensitive` and may not be shown to a hosted model;
  - the gap decides how many chats there are, and each bad setting is refused by its name; a person's own zone is kept;
  - a file that is not there, a text file, another app's database and a database in use are each refused, the last with the way out; `Copy` copies a database in use whole, leaves no log beside the copy and replaces no file; two workspaces need one named, and only that one is brought;
  - a database in the first build's schema is read;
  - the plan's odd cases: a reply read from the job's own copy, a turn waiting on a decision, a blank turn, a run of a routine that is gone, sources that are not links, routines with no hour or an unknown cadence.
- `go test ./internal/chat`: an imported chat keeps its times and its place in the list, is one branch, is found by the search and only in its own tenant, takes a later message, and goes when deleted; what cannot be a chat is refused and a caller's failure leaves no row.
- `go test ./internal/imported`: marks, models, source ids and totals read back; a source id is noted once; the mark goes with the chat and the source id stays; another tenant reads none.
- `go test ./tools/tenant`: the command refuses a database in use and says how to copy it, copies it, checks, imports, imports again to nothing, prints no word of the source, and refuses bad arguments.
- `verify`, the standing check `import` (`tools/verify/importcheck.go`): the proof above, and that `internal/importer` names no part that calls a model or a knowledge store or runs a turn. The `offline` check holds it to no network package.

Two faults were put in by hand and each was caught: the mark left off the list of chats (the proof), and a routine that is here already planned again (three tests).

## Not here yet

- **The owner's own import.** The integrator runs it, with the runbook, after this is merged. Until he has seen his chats the item is provisional.
- **Recaps for every imported chat at once.** Each gets one at its next turn, or when its recap route is asked. A sweep belongs with the background jobs.
- **A view of the events for each reply.** The counts are kept for each chat; the screen shows them as one line.
- **The files of New Keystone's built apps**, which are on its disk and not in its database.
- **Notebook entries.** They are counted and left (decision [0514](decisions/0514-memories-drafts-and-built-apps-are-left-in-new-keystone.md)).
- **Another source.** `import_sources` names its source, so another app's export could be brought the same way; only New Keystone's is read today.
