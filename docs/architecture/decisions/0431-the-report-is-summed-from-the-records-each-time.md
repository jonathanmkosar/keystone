# 0431. The usage report is summed from the records each time it is read, by one function, and a breakdown adds up its rest

November 25, 2026 · decided in P11-02

## The question

The plan's proof for usage and cost is that totals equal the sum of the usage records, and for phase 11 that spend matches the usage records. The report shows a total and three breakdowns, on a screen and on the command line, and a breakdown can have more lines than a screen should show. Should the report keep running totals as calls are made, should the screen and the command line each sum for themselves, and what happens to the lines past the limit?

## The decision

- `usagereport.Read` sums the period's records each time a report is read, and keeps nothing. The total and the three breakdowns come from the same records in one pass, and each record lands on exactly one line of each breakdown.
- `GET /api/v1/usage` and `tools/tenant usage` both call `Read`.
- The cap's spend is read with `usage.Spent`, the function the gate reads before a paid call, and the reserve's with `usage.SpentOn`, as the memory pass reads it.
- A breakdown shows `usage.report_rows` lines, costliest first, and adds up the rest in one more line that says how many it holds. It does not drop them.

## Why

- A running total is a second record of the same money, and the two can drift: a write that fails half way, a restore, a record added by a later writer. The records are append-only, so a sum of them is the truth by construction, and a month for one person is a few thousand rows.
- One function means the screen and the terminal cannot disagree, and the check need only prove one.
- Reading the cap's spend with the gate's own function means the screen shows what the gate enforces, not a third computation.
- Dropping the lines past the limit would make the breakdown stop adding up to the total, which is the proof. Adding them up keeps it, and the person can ask for more rows.

## What it costs

- Every report reads every record of its period. A range is bounded by `usage.report_max_days` (366 by default).
- A past month is shown against today's cap and threshold: settings keep no history.
- The line that adds up the rest has no key and links nowhere.
