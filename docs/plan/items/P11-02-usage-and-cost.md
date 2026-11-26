# P11-02. Usage and cost

Status: provisional
Lane: core + interface
Depends on: P3-08, P4-01
Owner gate: Approval of the settings mockups (P1-07), for the screen only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Spend against the cap, by model, chat and job kind.
Proof: Totals equal the sum of usage records.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

Built by the core lane on the branch `core/p11-02-usage-and-cost`, cut from `reboot` at 8106f15, on the web shell (P4-01), whose only open part is the owner's approval of the mockups.

**The report**, a new package `internal/usagereport`:

- `Read(ctx, store, registry, ask, now)` is the one function behind `GET /api/v1/usage` and `tools/tenant usage`, so the two cannot disagree. It reads the person's settings, works out the period, reads the usage records in it once, and sums them into a total and three breakdowns: by model (and class), by chat, and by kind of work. The total and every row carry calls, input, output, cache read and cache write tokens and cost, split into local, hosted and trusted.
- It reads the usage records and nothing that keeps a second running total. Records are not changed and nothing about how they are written changes.
- Placing a record. A record carries a request id. It belongs to a background task when a task has that request id, and is then a scheduled run when a schedule run started the task; its chat is the task's work chat. Otherwise its chat is that of the turn with that request id, or failing that the chat named in the path of the API call that set it off, from the audit log. Otherwise it is "not in a chat you still have". Its kind of work is the record's own kind (chat, title, summary, memory, embedding, or any other) unless it belongs to a task. Compaction and recaps both write `summary` and cannot be told apart.
- A breakdown shows the costliest `usage.report_rows` rows and folds the rest into one row, so the rows always add up to the total.
- The cap: the calendar month's spend on models that are not local (`usage.Spent`, what the gate reads), the cap, what is left, the warning threshold and the state, as configured. The memory reserve and what the memory pass has spent of it this month. A cap that does not read is reported as such.
- The period: `month=2026-11`, or `from` and `to` dates in UTC, both days included, at most `usage.report_max_days` apart, or `period=this_month`, `last_month`, `last_7_days` or `last_30_days`. None means `usage.report_period`.
- A migration, `20261125T222300_usage_report.sql`, adds an index on `turns(request_id)`, so placing a record is a lookup.
- The route `GET /api/v1/usage`, a person's own tenant only, added in `internal/app/app.go` with one line. `tools/tenant usage` prints the same report, for a month or a date range.
- Settings in `settings/core.json`: `usage.report_period`, `usage.report_rows`, `usage.report_max_days`.

**The screen**, provisional on the settings mockups (P1-07): Settings → Usage at `/settings/usage`, which takes the place of the Spend placeholder (`/settings/spend` goes there). The month's spend against the cap as a bar drawn in inline SVG from the tokens, with the warning threshold marked; what is left; the memory reserve; a period picker; the three breakdowns as tables sorted by cost, each row with a small bar, and a chat row linked to its chat. New files: `UsageSettings.tsx`, `usageCalls.ts`, `usage.css`.

**The proof**, as tests in `internal/usagereport` on generated records spanning models of each class, chats, background tasks, scheduled runs, kinds of work, and the October and November boundary in UTC: the totals equal the sum of the records; for each breakdown the rows sum to the total, field by field and class by class, to the millionth of a dollar; the cap's spend equals the month's cost on models that are not local; folding keeps the sums. A `verify` check, `usage report`, in a new file: the real server, the test tenant's real turns and generated records across a month boundary, every report the API gives compared with sums taken straight from the table, and a second tenant sees none of it.

Left for later: the per-run budgets of unattended work (P8-05) and the spend line on Operations (P11-03) read the same records and are not built here.

### Result (core lane, November 25, 2026)

Provisional on the settings mockups (P1-07) for the screen; the server side is complete and proven.

- Built as planned: `internal/usagereport` (`Read`, `Resolve`, `Records`, `Summarise`, the placing of a record by its request id, the cap and the reserve, the route `GET /api/v1/usage`), `tools/tenant usage` rewritten on `Read`, migration `20261125T222300_usage_report.sql` (an index on `turns(request_id, seq)`), three settings in `settings/core.json` (`usage.report_period`, `usage.report_rows`, `usage.report_max_days`), and Settings → Usage (`UsageSettings.tsx`, `usageCalls.ts`, `usage.css`), which took the place of the Spend placeholder. Decisions 0430 to 0432.
- The proof: `TestTotalsEqualTheSumOfTheRecords` in `internal/usagereport`, on 900 generated records over five models of three classes, chats, a background task, a scheduled task, a recap asked for through the API, a deleted chat and eight kinds of work, across midnight UTC on 1 November 2026: for seven periods the total equals the records summed by hand, every breakdown adds up to the total field by field and class by class, every line is exactly its records, and the cap's spend is the month's cost on models that are not local. The standing check `usage report` compares the API's reports with sums taken straight from the table on the real server. `verify` is 27 of 27.
- Not seen in a browser: the screen type-checks and builds, and the route answered the report on a server started from this branch; signing in to look at the screen was left to a person (see the journal).
- Compaction and recaps share one kind of work, "Summaries and recaps": the records do not tell them apart (decision 0430).
- `web/dist/` is not committed: the integrator rebuilds it at the merge.
- See `docs/journal/2026-11-25-core-011.md` and `docs/architecture/usage-report.md`.

