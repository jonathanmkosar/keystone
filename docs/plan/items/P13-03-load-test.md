# P13-03. Load test

Status: done
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

Built so far (commit 61eef73): the generator and its proof, the measurements, the budgets, the Monitor comparison, the command and the standing check, and two fixes the first large loads led to (past-chat search, decision 0530; a request's usage records read by their index). Left: the full run, the rooms, and the write-up.

## Result

Done on November 26, 2026 (the full runs were made after midnight UTC, so their table is dated the 27th). `internal/loadtest` writes three years of invented chats for one person, 102,416 turns and 204,832 messages with a 600-turn chat, and about 4,000 turns for each of five more, by a fast path that `TestTheFastPathLeavesWhatARealTurnLeaves` proves leaves, row for row, what the real handler leaves. On the real server in memory, with the fake provider, all thirteen times are inside their budgets at the 95th percentile, and all 21 figures Operations reports match the test's own counts. `go run ./tools/loadtest` is the full run, a quarter of an hour; the small profile runs as the standing check `load test`, and `verify` is 39 of 39.

Found and fixed, each with a test: past-chat search took 1.2 to 2.5 seconds for a common word over 100,000 messages, and now takes 30 to 80 ms (decision 0530, `chats.search_rank_max`); a request's usage records were found by reading every record; and the home screen's notice read a whole usage report for two sums, inside its budget by a tenth, and is now a third of the time. Reported and not changed, inside their budgets: every API call opens the person's store anew, and context packing reads a long chat more than once.

The sizes: a database is about 70 KB a turn, 7.16 GB for the three years, 58% of it the audit log's copies of each request and 25% the streamed pieces of every reply. `operations.database_room_mb` goes from 2048 to 10240 (decision 0533); the files' and the artifacts' rooms are kept. Two questions for the owner come out of it: whether a turn's events are removed once it has ended, and whether the audit log holds every request whole for good.

The doc, with what is measured and how, every finding and the rooms: `docs/architecture/load-test.md`. Decisions 0530 to 0533. Journal: `docs/journal/2026-11-26-core-014.md`. The last full run's table follows.

<!-- load test: the last full run, written by go run ./tools/loadtest -->

The full run of November 27, 2026 at 03:28 UTC, on windows/amd64, 16 logical processors, go1.26.0, AMD64 Family 25 Model 117 Stepping 2, AuthenticAMD. Every time is inside its budget, and every figure of the Monitor matches the test's own.

The load: 3 years, 5,001 chats, 102,416 turns, 204,832 messages, 110,946 model calls and 438,169 audit entries for the first person (one chat of 600 turns), with 200 uploads and 125 artifacts; 5 more people with 200 chats each. Seed 20261126. Writing it took 10m24s, measuring 35s, the whole run 13m52s.

| Measurement | Runs | Median | 95th percentile | Budget | Held to it (95th percentile) |
| --- | --- | --- | --- | --- | --- |
| Context packing, the long chat: a message sent to its model call opening | 30 | 110ms | 165ms | 250ms | inside |
| Context packing, a chat with attached chats, an upload and a project | 30 | 40ms | 55ms | 250ms | inside |
| Past-chat search, the model's `search_chats`: the tool call to its result | 30 | 48ms | 269ms | 500ms | inside |
| Past-chat search, `GET /api/v1/chats/search` | 30 | 47ms | 117ms | 200ms | inside |
| The chat list's first page, `GET /api/v1/chats` | 30 | 9.3ms | 14ms | 100ms | inside |
| The long chat's page, `GET /api/v1/chats/{id}` | 30 | 35ms | 42ms | 400ms | inside |
| First streamed words, one person: a message sent to the first words | 30 | 25ms | 47ms | 300ms | inside |
| First streamed words, every person sending at once | 180 | 80ms | 107ms | 500ms | inside |
| Streaming a reply, one person: the first words to the last | 30 | 69ms | 126ms | 1s | inside |
| Streaming a reply, every person sending at once | 180 | 26ms | 296ms | 1.5s | inside |
| The usage report, `GET /api/v1/usage` | 30 | 55ms | 66ms | 300ms | inside |
| The Operations report, `GET /api/v1/operations` | 30 | 38ms | 50ms | 500ms | inside |
| The home screen's notice, `GET /api/v1/operations/watch` | 30 | 31ms | 42ms | 150ms | inside |

| On disk, the first person | Size |
| --- | --- |
| The database, `keystone.db` | 7.16 GB |
| The files folder, 200 uploads and a project file | 42.9 MB |
| The artifacts folder, 125 artifacts in two versions each | 3.3 MB |
| Each other person's database, the mean of 5 | 287.7 MB |

| The database by table, with its indexes | Rows | Size | Share |
| --- | --- | --- | --- |
| `audit_log` | 438,169 | 4.12 GB | 58% |
| `turn_events` | 15,237,117 | 1.79 GB | 25% |
| `turn_contexts` | 102,597 | 471.4 MB | 6% |
| `message_search` | 205,134 | 319.7 MB | 4% |
| `messages` | 205,194 | 318.5 MB | 4% |
| `turns` | 102,567 | 81.2 MB | 1% |
| `uploads` | 201 | 43.0 MB | 1% |
| `turn_messages` | 205,194 | 17.6 MB | 0% |
| `usage_records` | 111,206 | 17.0 MB | 0% |
| `chat_recap_search` | 5,001 | 3.8 MB | 0% |
| every other table | | 6.0 MB | 0% |

| The Monitor, `GET /api/v1/operations` | Operations reports | The test's own count | |
| --- | --- | --- | --- |
| After the load is written, database, bytes | 7848205280 | 7848205280 to 7848205280 | matches |
| After the load is written, files, bytes | 44912640 | 44912640 | matches |
| After the load is written, artifacts, bytes | 3448415 | 3448415 | matches |
| After the load is written, the cap, spent this month, millionths of a dollar | 20005991 | 20005991 | matches |
| After the load is written, the cap, millionths of a dollar | 100000000 | 100000000 | matches |
| After the load is written, calls to load-hosted in the last 24h0m0s | 91 | 91 | matches |
| After the load is written, cost of load-hosted in the last 24h0m0s, millionths of a dollar | 951827 | 951827 | matches |
| After the load is written, median latency of load-hosted, milliseconds | 3274 | 3274 | matches |
| After the load is written, calls to load-local in the last 24h0m0s | 1 | 1 | matches |
| After the load is written, cost of load-local in the last 24h0m0s, millionths of a dollar | 0 | 0 | matches |
| After the load is written, traces whose calls, tokens and cost are the test's own | 20 of 20 | 20 of 20 | matches |
| After the measurements, database, bytes | 7862594528 | 7862594528 to 7862594528 | matches |
| After the measurements, files, bytes | 44938540 | 44938540 | matches |
| After the measurements, artifacts, bytes | 3448415 | 3448415 | matches |
| After the measurements, the cap, spent this month, millionths of a dollar | 20431991 | 20431991 | matches |
| After the measurements, the cap, millionths of a dollar | 100000000 | 100000000 | matches |
| After the measurements, calls to load-hosted in the last 24h0m0s | 211 | 211 | matches |
| After the measurements, cost of load-hosted in the last 24h0m0s, millionths of a dollar | 1377827 | 1377827 | matches |
| After the measurements, calls to load-local in the last 24h0m0s | 140 | 121 or more | matches |
| After the measurements, traces whose calls, tokens and cost are the test's own | 20 of 20 | 20 of 20 | matches |
| After the measurements, the newest trace | d11ccfbed02a9c6e | d11ccfbed02a9c6e | matches |

<!-- load test: end of the last full run -->

