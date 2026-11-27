# 0531. The load test's chats are written by a fast path, proven row for row against the real handler

November 26, 2026 · decided in P13-03

## The question

The load test (P13-03) needs years of chats for one person: thousands of chats and hundreds of thousands of messages, with everything a turn leaves beside them. Should it send every turn through the real handler, call the store functions the handler calls, or write the rows itself?

## The decision

**A fast path writes a turn's rows directly**, in transactions of fifty chats, with dates of its own (`internal/loadtest/fast.go`):

- `chats`, `messages` and the two search tables, as `chat.Create`, `chat.Append` and `chat.Rename` leave them;
- `turns`, `turn_events` (every streamed piece, cut as the fake provider cuts a reply), `turn_messages`, `turn_contexts` and `chat_styles`, as `internal/turn` leaves them;
- for each model call, the chat's and a hosted chat's title call, two `audit_log` entries and one `usage_records` row; for each API call, two audit entries.

**What a call carries is not written by hand.** The request in the audit log and the record in `turn_contexts` come from the real context builder, `contextbuild.Build`, given what a plain turn gives it: the person's settings, the model's entry, the chat's messages so far, and the tools a real turn of the same person offered, read back from that turn's own audit entry (the probe: one real turn, sent first). An event's data is the turn package's own type, marshalled; a message is the seam's own encoding; a recap's request is `recap.Request`.

**The proof is a comparison of two databases.** `TestTheFastPathLeavesWhatARealTurnLeaves` sends a sample of six chats, on both models, through the real handler for one person, with the fake provider scripted to say each planned reply; writes the same six with the fast path for a second person; and compares every table of the two, row by row (`agree.go`). Ids made at random are replaced by their place in the order they were made, times by a mark, and three columns are left out: how long a call took (twice) and the month it fell in. Ids the database counts are compared as they are, so the two paths must also write in the same order. Seven tables are left out, each with its reason in the code: the person's own id, password and session, the migrations' times, the sampler's snapshots, SQLite's counters, and `person_map`, a one-row cache the first real turn keeps. The test then changes one word in one event and expects one difference.

**The rare things go through the real store functions**: uploads (`uploads.Add`), artifacts in two versions (`artifacts.Create`, `artifacts.Update`) and recaps (`recap.Save`), and the measured project, its file, its chat's upload and attached chats through the API.

## Why

- **The real paths take no date.** The chat record, the turn, the audit log and the usage records stamp a row with the moment it is written; the audit log on purpose, so an entry cannot be placed in the past. A load written through them would be years of chats made in one minute: every call would sit in Operations' last day and the usage report's current month, and the month index, the health window and the cap would be measured on a shape no person has.
- **Speed.** A real turn is some thirty transactions. The fast path writes about 300 turns a second, with their requests built by the real builder; the full profile's hundred thousand turns take minutes, not an hour.
- **A hand-written shape drifts.** Taking the request from the real builder and the events from the turn's own types means a change to either changes the load with it, and what is still written by hand (the order of the rows, the columns) is held by the comparison, which fails on the first row that differs.

## What it costs

- A part that makes a plain turn write a new row, or a new column, fails the comparison until the fast path writes it too. That is the point, and it is one more place to touch.
- Three things are written that the comparison does not cover, and the doc says so: the recap's model call (a real recap is made two idle minutes after a turn, so the load people run with `chats.recap` off, and the call is written as a summary call under the chat's last request, with the recap package's own request); a hosted chat's history on a model whose entry names an address that can never resolve, answered by the fake provider; and the long chat, which the builder packs by leaving its oldest turns out, where the real server would by then have written a summary of them (the measured turns in it do set compaction off, for real).
- The audit log's times are written, not stamped. Only the load test does this, in a throwaway folder; nothing in the server can.
