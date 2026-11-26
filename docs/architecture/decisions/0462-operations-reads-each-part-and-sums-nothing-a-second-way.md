# 0462. Operations reads each part with its own functions or narrow queries, and sums nothing a second way

November 26, 2026 · decided in P11-03

## The question

Operations shows jobs, schedules, model health, recent traces and limits, each kept by another part: the task runner, the scheduler, the usage records, the audit log, the usage report. Should Operations keep its own running counts, ask each part through a function, or read each part's tables?

## The decision

Operations keeps nothing of its own but the limits' snapshots, and reads each part as close to the source as it can:

- **The month against the cap and the memory reserve** come from `usagereport.Read` with the month, the function behind Settings → Usage, so the two screens cannot disagree. The cap's watch level is the cap's own warning threshold, `usage.cap_warn_percent`, so the home screen's notice and the cap's warning start at the same spend.
- **Tasks** come from `jobs.List` and `jobs.Tally`, new in `internal/jobs/list.go`, which read the jobs table through the package's own row reader, so a task the pause switch holds reads as paused, as on the task routes. A task's spend, and a run's budget, are summed from the usage records of its request id (`unattended.Spent`), as the budget's gate sums them, not read from the task's row.
- **Schedules** come from the scheduler's own `List`, handed to the route, so the zone, the switches and the newest run are what the Scheduled screen shows.
- **Model health and traces** read the usage records with `usage.List`, and the audit log and the turns table with narrow queries of a few columns (a call's outcome, a tool's name, a failed call's words), as the usage report reads the turns and jobs tables to place a record (decision [0430](0430-a-usage-record-is-placed-by-its-request-id.md)). A trace's tokens and cost are its usage records', so they equal the turn inspector's and the usage report's.
- Every text shown from the audit log, a turn's error, a task's error or a run's goes through `audit.Scrub` on the way out, though it was scrubbed when it was written.

## Why

- A second running count drifts. Phase 11's rule is that spend matches the usage records, and the usage report is already the one sum of them.
- `audit.List` returns whole entries, and a model call's entry holds its whole request; reading twenty traces through it would read megabytes to show a name and an outcome. A narrow query reads what is shown, and only reads.
- The task row's spend is written from its turns for the task's own screen; the gate reads the usage records. A budget shown against anything but what the gate reads could say "room left" when a call is about to be refused.

## What it costs

- The narrow queries know the column names of the audit log, the turns and the jobs tables. A change to those tables must look here too; the tests in `internal/operations` write rows as the owners write them and fail when they no longer read.
- `jobs.List` is one more exported function in `internal/jobs`, in a file of its own.
