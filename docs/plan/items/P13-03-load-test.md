# P13-03. Load test

Status: doing (core)
Lane: core
Depends on: P11-03
Owner gate: none
Scope: Years of chats for one tenant and several tenants at once; context packing, search and streaming times measured.
Proof: Times stay inside their budgets and the Monitor figures match the test's own.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `core/p13-03-load-test`, cut from `reboot` at cfa1d15. Run now by the integrator's decision: P11-03's open parts are its mockups and its room sizes, and this item measures the rooms.

Everything is new code in a new package, `internal/loadtest`, a new command, `tools/loadtest`, and a new settings file, `settings/loadtest.json`. A fix for a slow path is the smallest local change in the part that owns it, with a test.

**The generator.** For one person, years of chats: thousands of chats and hundreds of thousands of messages by default (`loadtest.chats`, `loadtest.turns_per_chat`, `loadtest.words_per_turn`, `loadtest.years`, and one long chat of `loadtest.long_chat_turns`), dated across the years, with recaps, uploads, artifacts, a project, usage records and audit entries in proportion; and several more people at once (`loadtest.tenants`, five by default) each with a smaller load. Words are invented, built from syllables by a seeded generator (`loadtest.seed`), so a run is repeatable and holds nothing real.

- A real turn's rows are written by a **fast path**: direct inserts in large transactions, because the real paths take no date, and a load dated today would put every call in Operations' last day. It leaves the rows a real turn leaves: `chats`, `messages` and their search rows, `turns`, `turn_events` (every streamed piece), `turn_messages`, `turn_contexts`, `chat_styles`, one `usage_records` row, and four `audit_log` entries a turn, the model's with the whole request and reply as the real ones hold them.
- **Proof the two agree**: a test sends a small sample of chats through the real handler with the fake provider, generates the same sample with the fast path for a second person, and compares the two databases table by table, every column but ids, times and durations.
- The rare things (uploads, artifacts, the project and its file, recaps) go through the real store functions.

**The measurements**, against the real handler (`app.Open`, in memory, the fake provider on the seam), each as a median and a 95th percentile over `loadtest.runs` runs:

1. Context packing: from a message sent to its model call opening, in the long chat, and in a chat with attached chats, an upload and a project.
2. Past-chat search: the model's `search_chats` (from the tool call to its result, in a real turn) and `GET /api/v1/chats/search`.
3. The chat list's first page, and the long chat's page.
4. Time to the first streamed words, and the time to stream a reply, for one person and for every person sending at once.
5. `GET /api/v1/usage`, `GET /api/v1/operations` and `GET /api/v1/operations/watch`.
6. The sizes the load leaves: the database with a share for each table, the files folder and the artifacts folder.

**Budgets.** One time budget for each measurement, in `settings/loadtest.json`, chosen from what a person feels (a decision record says why), held against the 95th percentile. A measurement over budget is a finding: the cause is found and fixed where it is, with a test; no budget is raised to pass.

**Monitor agreement.** For the loaded person, `GET /api/v1/operations` must show what the test itself counted: the database's bytes on disk, the bytes written to files and to artifacts, the cap's spend this month, each model's calls and median latency over the last day, and the measured turns as the newest traces with their calls and tokens.

**Two sizes.** `go run ./tools/loadtest` is the full run (minutes; not in `verify`): it prints the table and writes it into this item's Result and `docs/architecture/load-test.md`. A small profile runs in a few seconds as a new standing check, `load test` (one line in `checks`), with the same generator, measurements and monitor agreement.

**Rooms.** From the full run's sizes, a recommendation for `operations.database_room_mb`, `operations.files_room_mb` and `operations.artifacts_room_mb`; the defaults change only if the measured sizes show them wrong, with a decision record.

Left to do, in this order: the generator and the agreement proof; the measurements, budgets and monitor agreement; the command and the standing check; the full run and any fixes; the write-up.

