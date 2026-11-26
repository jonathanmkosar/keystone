# P11-03. Operations and Monitor

Status: doing (core)
Lane: core + interface
Depends on: P8-01, P4-01
Owner gate: Approval of the settings mockups (P1-07), for the screens only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Jobs, schedules, model health, recent traces, and where the install is running out of room.
Proof: Each limit shows its share used and trend; a crossed watch level shows on the home screen.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

Built by the core lane on the branch `core/p11-03-operations`, cut from `reboot` at df91864, on the web shell (P4-01), whose only open part is the owner's approval of the mockups. Borrowed in spirit from New Keystone's Operations screen and drawn from `design/screens/Operations.html`.

**The report**, a new package `internal/operations`, read-only over what other parts keep, with one table of its own:

- `Read(ctx, store, reg, src, now)` is the one function behind `GET /api/v1/operations`. Five parts:
  - **Jobs**: the person's background tasks and scheduled runs by state (queued, running, paused, waiting for approval, done, stopped, failed), the newest `operations.jobs_shown`, the stuck ones (open, not paused and not waiting, with a round that began more than `operations.stuck_after` ago) and the ones that failed within the health window. Listed through a new `jobs.List` and `jobs.Tally` in a new file, `internal/jobs/list.go`, so the paused status is read as the routes read it.
  - **Schedules**: each schedule with its recurrence in words, next run, last run and its outcome, enabled, held; from `Scheduler.List`, handed to the route as a function.
  - **Model health**: every model in the person's registry, and any other that took a call in the window: calls, ok, errors, refusals, stopped, median time and cost over `operations.health_window`, from the usage records, with a state (good, errors at `operations.model_error_watch_percent`, failing when every call failed, no calls) and the last error's kind and words from the audit log, passed through `audit.Scrub`, so never a key.
  - **Recent traces**: the newest `operations.traces_shown` request ids among the person's turns (a chat turn, a background task's rounds, a scheduled run): the chat and its newest turn (the link to the turn inspector), the models, model calls, errors and refusals, the tool steps by name and outcome, tokens and cost from the usage records of the request id, the outcome (the turn's or the task's status) and the error's words, scrubbed.
  - **Limits**, where the install is running out of room, each with its share used, its watch level, whether that level is crossed, and a trend: the monthly cap and the memory reserve (from `usagereport.Read` with the month, not computed again; the cap's watch level is its own warning threshold, `usage.cap_warn_percent`, so the two never disagree), the disk of the volume that holds the data folder (an install fact, shown to every person, with nothing from another tenant: the volume's used and free space, never the data folder's total, which with two people would tell one the size of the other's folder), the person's database, files folder and artifacts folder against the room set for each, and the per-run budget of each background task or scheduled run under way.
- **Trends** from snapshots the server keeps: a migration, `20261126T001900_operations.sql`, adds `operations_snapshots` (time, limit, used, of) to each tenant's database. A sampler under the turn runner records every tenant's limits at start and every `operations.snapshot_every` (an hour), skipping a tenant whose newest snapshot is younger, and removes snapshots older than `operations.snapshot_keep`. A trend is the snapshots within `operations.trend_window` and the reading now: its points, its growth per day (least squares), and when it would reach the limit at that rate, or that it would not before the limit starts again (the cap's and the reserve's month). A run's budget has no snapshots: its trend is its usage records, added up in order.
- `Watch(ctx, store, reg, src, now)` is the limits whose watch level is crossed, and `GET /api/v1/operations/watch` serves it for the home screen. Both routes read the same `Limits`.
- Settings in `settings/core.json`, none in code: `operations.snapshot_every`, `operations.snapshot_keep`, `operations.database_room_mb`, `operations.files_room_mb`, `operations.artifacts_room_mb` (install), and `operations.trend_window`, `operations.health_window`, `operations.jobs_shown`, `operations.traces_shown`, `operations.stuck_after`, `operations.model_error_watch_percent`, and a watch level for each limit, `operations.watch_disk_percent`, `operations.watch_database_percent`, `operations.watch_files_percent`, `operations.watch_artifacts_percent`, `operations.watch_reserve_percent`, `operations.watch_run_budget_percent` (tenant).
- The fixed rules: one tenant's store only (rule 1); every text shown from the audit log or a turn's error goes through `audit.Scrub` and the walk of `no keys in output` reads the routes (rule 4's records are read, never written or changed). The package enforces no fixed rule, so it may read settings.

**The screens**, provisional on the settings mockups (P1-07): an Operations page at `/operations`, linked from the sidebar and from a Settings tab, drawn from `Operations.html` with the tokens: tiles (running now, failed in the window, spend this month), needs attention, room to grow (each limit with a bar, a sparkline of its trend, growth per day, when it fills, and a pill), models, background tasks, schedules, and recent traces linked to their chat with the inspector open. A notice on the home screen when a watch level is crossed, after the "Running now" strip of `Home.html`. Bars and sparklines are inline SVG from the tokens, no chart library. New files `Operations.tsx`, `OperationsNotice.tsx`, `operationsCalls.ts`, `operations.css`; a line each in `App.tsx`, `Shell.tsx`, `Settings.tsx`, `Chat.tsx` and `Icon.tsx`.

**The proof**, as tests in `internal/operations`: generated snapshots over a week give each limit its share used and a trend with growth and a fill date; a crossed watch level is in `Watch` and in `GET /api/v1/operations/watch`; the cap equals `usagereport.Read`'s; model health counts and medians equal the records; a trace's tokens and cost equal its usage records; no key placed in an error reaches a reply; another tenant sees none of it. A `verify` check, `operations`, in a new file: the real server with the test tenant, generated snapshots and a lowered room, every limit with a share and a trend, the crossed one on the watch route, the cap equal to the usage report's, and a second tenant's report empty of the first's. A browser check signs in as the test tenant on a server of this branch and sees the notice on the home screen.

Left for later: an install-wide view for an admin (there is no admin role); retry and reconnect buttons on the rows that need attention; past-chat search time as a limit (no timing is recorded yet).

