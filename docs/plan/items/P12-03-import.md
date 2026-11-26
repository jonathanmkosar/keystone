# P12-03. Import from New Keystone

Status: doing (chats)
Lane: chats
Depends on: P4-02, P8-03
Owner gate: A copy of New Keystone's workspace database, and Jonathan's go-ahead to import it.
Built without the gate: The importer is built against a fixture in New Keystone's schema.
Scope: Import old chats and routines; routines become scheduled tasks; the morning brief runs here.
Proof: Imported chats open and search; the morning brief runs on schedule.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (chats lane, November 26, 2026)

Branch `chats/p12-03-import`, cut from `reboot` at a3d2864. The owner has given his go-ahead to import. The importer is built and proven against a database generated in New Keystone's schema with invented words; the owner's real database is never opened in this work, and the integrator runs the real import after the merge. New code goes in new files; every line changed in a shared file is listed in the journal.

What New Keystone keeps, read from its source (`keystone/store.py`, `routines.py`, `runtime.py`): one `workspace.sqlite` for every workspace, in write-ahead mode. It has no chats: a workspace is one continuing conversation, a row in `jobs` for each turn (the person's prompt, the reply, a status, a kind `chat` or `build`, the model picked), one or two rows in `messages` for each job, and rows in `events` saying what happened in it, as short safe words and never a tool's input or result. A routine is a saved prompt with a cadence (hourly, daily, weekdays, weekly on one day, once, or manual) in America/Chicago; the morning brief is the built-in one. Nothing in it is marked sensitive.

1. **The record of what was imported, a new package `internal/imported`**, with migration `20261126T022700_imports.sql`: `imports` (one row for each run that wrote something), `import_sources` (every source id brought in, keyed by source, workspace, kind and id, kept for good so a deleted chat does not come back), `imported_chats` (the mark a chat wears: where from, when, how many turns, the models named, the steps New Keystone recorded, in words) and `imported_messages` (the model named for a reply). The last two go with their chat.
2. **Writing a chat with its own times**, a new file `internal/chat/imported.go`: `chat.Import` writes a whole chat, its title, its messages in a row and their search rows in one transaction, with the times given, and runs the caller's function in the same transaction, so a chat and the record of where it came from are written together or not at all.
3. **The importer, a new package `internal/importer`**:
   - *The source* is opened read-only and immutable, after a check that it is a whole copy (no write-ahead log beside it) in New Keystone's schema. Older databases that lack later columns are read with those columns empty.
   - *Turns into chats.* A turn started by a routine becomes a chat of its own, titled by the routine and the date, as a scheduled run's chat is here. A build becomes a chat of its own. The rest, the continuing conversation, is cut into chats wherever more than `chats.import_chat_gap` (1h) passed between turns; a chat is titled by the first words of its first message. Messages keep their order, roles and original times, as plain text; a reply's web sources are a list at its end; a text attachment follows the message it was sent with. The model named for each reply is kept with the message. A turn that failed or was cancelled keeps the person's message and has no reply. A turn not finished when the copy was made is left for a later run.
   - *Tool events* cannot be recorded steps: New Keystone kept no tool's input or result, and a step in the chat record is a call and its result. They are counted by kind and summarised in the chat's mark (decision record).
   - *Routines into schedules*, through `internal/schedules`' own `Create`: each active or paused routine with a cadence Keystone has becomes a schedule, paused, with its name, prompt and times kept. When the person has no `agents.time_zone`, it is set to New Keystone's zone (`chats.import_time_zone`, America/Chicago) so 07:00 stays 07:00. Manual routines, routines only proposed, deleted ones and one-time ones whose time has passed are left out and counted.
   - *Left out and counted in the report*: memories (those New Keystone sent to Waymarks are already in the store Keystone reads; the import writes to no knowledge store and calls nothing), decisions, calendar and email drafts, proposals, release feedback, trials and the wiki tables. New Keystone's artifacts are built app releases whose files are not in the database, so they are not Keystone artifacts: each one's summary and state go in the note of its build's chat.
   - *Sensitive content.* New Keystone marked nothing, read one Waymarks space and sent every chat to whichever model it was set to, so imported chats are not held by default. `chats.import_hold` can hold the chats in which New Keystone read the wiki (`wiki`) or every imported chat (`all`) to local or trusted models, written in the same transaction as the chat.
   - *Twice is once.* Source ids already in `import_sources` are skipped before anything is grouped. A run writes a `started` and an end entry in the tenant's audit log, and calls no model, no knowledge store and no network.
   - *The dry run* (`--check`) writes nothing and prints counts only: what would be imported, what is already there, what is left out and why. No title, name or message text is ever printed, by the check or the import.
4. **The command**: `tools/tenant import [--check] [--workspace <name>] <tenant-id> <copy of workspace.sqlite>`, and `tools/tenant import-copy <workspace.sqlite> <new file>`, which makes a whole copy of a database that is in use (`VACUUM INTO`, read-only on the source).
5. **Shown as imported**: `GET /chats` and `GET /chats/{id}` give an imported chat its mark (`imported`), and an imported reply its model (two added lines in `internal/turn/handlers.go`, one field on `turn.Chat`); a small mark on the chat's row and a line at the top of the chat (`web/app/src/Imported.tsx`, with one added line each in `Shell.tsx` and `Chat.tsx`). Imported chats are found by the chat search and by `search_chats` from their titles and messages at once; each gets a recap as older chats do, at its next turn or through `POST /chats/{id}/recap`.
6. **The proof**: Go tests on a generated source (`internal/importer/importertest` builds one in New Keystone's schema); a standing check `import` in `tools/verify/importcheck.go` on the real server: imported chats open through the API in order with their times and are found by `search_chats`; the imported morning brief is a paused schedule that runs, with the fake provider, once switched on; a second import adds nothing; the other tenant holds nothing of it; the import made no model call.
7. **The write-up**: `docs/architecture/import.md`, decisions from 0510, the runbook `docs/runbooks/import-new-keystone.md`, the journal, the status (`provisional` until the integrator has run the owner's real import and he has seen his chats).

Left for later: recaps for all imported chats in one go (a sweep belongs with the background jobs); a per-reply view of the steps New Keystone recorded; importing the files of New Keystone's built apps.
