# Operations and Monitor

Folders: `internal/operations`, a list of every task in `internal/jobs/list.go`, and the screens in `web/app/src/` (`Operations.tsx`, `OperationsNotice.tsx`, `operationsCalls.ts`, `operations.css`)

What is running for a person, how it is doing, what went wrong, and where the install is running out of room. The plan's words: Operations and Monitor pages show jobs, schedules, model health, spend against the cap, and where the install is running out of room. Built in P11-03, borrowed in spirit from New Keystone's Operations screen. The screens are provisional: see "Provisional" at the end.

The package reads what other parts keep and changes none of it. Its one table holds snapshots of each limit, so a limit has a trend.

## One function, and one for the home screen

```go
rep, err := operations.Read(ctx, store, registry, operations.Sources{Schedules: scheduler.List}, time.Now())
w, err := operations.Watch(ctx, store, registry, time.Now())
```

`Read` is the one function behind `GET /api/v1/operations`. `Watch` is the limits whose watch level is crossed, behind `GET /api/v1/operations/watch`, which the home screen reads. Both read the limits with `Limits`, so the notice and the report never disagree.

| Exported | What it is |
| --- | --- |
| `Read(ctx, s, reg, src, now)` | The report for one tenant: `Report`, with `jobs`, `schedules`, `models`, `traces`, `limits` and `crossed`. |
| `Watch(ctx, s, reg, now)` | `WatchReply`: the crossed limits. |
| `Limits(ctx, s, reg, now)` | Every limit with its share, watch level, state and trend. |
| `Sample(ctx, s, reg, keep, now)` | Records where each limit stands now, but a run's budget, and removes snapshots older than `keep`. |
| `SampleDue(ctx, in, reg, cfg, now, log)` | Samples every tenant whose newest snapshot is older than `cfg.SnapshotEvery`. |
| `Start(goer, in, cfg, log, now)` | The sampler, as a goroutine under the turn runner: at once, then every `cfg.SnapshotEvery`. |
| `Keep(ctx, s, snaps)`, `Snapshots(ctx, s, key, since)`, `Newest(ctx, s)` | Writes snapshots, reads one limit's, and the time of the newest. A check or a drill generates snapshots with `Keep`. |
| `ReadSettings(ctx, s, reg)`, `ReadConfig(get)`, `DefaultConfig()` | The report's settings as they stand for a tenant; the sampler's install settings. |
| `Routes(Options)` | The two routes. `Options` holds the scheduler's `List`, the log and a clock. |
| `Report`, `JobsPart`, `TaskLine`, `SchedulesPart`, `ScheduleLine`, `RunLine`, `ModelsPart`, `ModelHealth`, `CallError`, `Trace`, `ToolStep`, `Limit`, `Trend`, `Point`, `Snapshot`, `WatchReply` | The shapes, below. |

The package imports the jobs, schedules, unattended runs, usage records, usage report, model registry, audit log, tenant store, tenant settings and the API shell. It enforces no fixed rule, so it may read settings. A person reaches only their own tenant because each route acts on the store the shell hands it (fixed rule 1).

## Jobs

`jobs` is the person's background tasks and scheduled runs ([jobs.md](jobs.md), [schedules.md](schedules.md)):

| Field | Meaning |
| --- | --- |
| `paused` | The switch that pauses every agent and schedule, `agents.paused` ([unattended-spend.md](unattended-spend.md)). |
| `counts` | How many tasks are `queued`, `running`, `paused`, `done`, `stopped` and `failed`, as the task routes show a status, and `waiting` for the open ones a call of which waits for the person's approval. |
| `open` | Queued, running or paused. |
| `newest` | The newest `operations.jobs_shown`, as `TaskLine`s. |
| `stuck`, `stuck_after` | Open tasks, not paused and not waiting for the person, whose current round began longer ago than `operations.stuck_after`. |
| `failed`, `failed_count` | Tasks that failed within `operations.health_window`, newest first. |

A `TaskLine` is a task's id, title, `scheduled`, status, waiting, progress, its two chats and round's turn, rounds and steps, budget and spend, its error's words (through `audit.Scrub`), and its times.

The list comes from `jobs.List` and `jobs.Tally`, new in `internal/jobs/list.go`: a task across every chat, read through the package's own `scanJob`, so a task the pause switch holds reads as paused here as it does on the task routes; `List` adds the task's request id, whether a schedule run started it, and when its current round began.

## Schedules

`schedules` is the person's schedules as the Scheduled screen lists them, through the scheduler's own `List`, handed to the route: `paused` (the schedules' switch), `zone`, and for each schedule its name, recurrence in words, enabled, held, next due time in the person's zone, and its newest run (when it started, its status, late and why, how many due times it stood for, its error through `audit.Scrub`, its chat, its spend). `enabled` counts the schedules not paused, and `failing` those whose newest run failed.

## Model health

`models` covers `operations.health_window` (`since`, `window`). One `ModelHealth` for each model in the person's registry, in name order, then each model that took a call in the window and has left the registry since:

| Field | Meaning |
| --- | --- |
| `name`, `label`, `class`, `where` | `where` is the adapter and the host it calls: no scheme, path, query or user, so nothing that could carry a key. |
| `in_registry`, `problem` | Whether it is still in the registry, and why its entry is refused today. |
| `calls`, `ok`, `errors`, `refusals`, `stopped` | Its usage records in the window, by outcome. |
| `error_percent` | Errors of the calls that were sent (all but refusals). |
| `median_ms` | The median time of the calls that were sent, from start to end. A call's first words are not recorded, so there is no "first words" time. |
| `cost_micros` | What its calls cost. |
| `state` | `idle` (no calls), `refused` (none sent), `failing` (every one sent failed), `errors` (at or past `operations.model_error_watch_percent`), or `good`. |
| `last_error` | Its newest failed call in the window: `at`, `kind` and `message`, from the audit entry that ended the call under the record's request id, each through `audit.Scrub`. The audit log already scrubbed it when it was written; it is scrubbed again on the way out. |

## Recent traces

`traces` are the person's newest `operations.traces_shown` request ids among their turns, newest first. A request id ties together everything one API call set off ([audit.md](audit.md)), so a trace is a chat turn with every call made for it (the reply, its title, a summary, the memory pass), or every round of a background task or scheduled run, whose turns all carry the task's own request id.

| Field | Meaning |
| --- | --- |
| `request_id`, `kind`, `title` | `chat`, `task`, `schedule` or `subagent` (from `unattended_runs`), and the chat's title or the run's. |
| `chat_id`, `turn_id`, `run_id` | Where the turn inspector opens: the chat and its newest turn of the trace, or for a run its own chat and its round. Empty when the chat is gone. |
| `turns` | How many turns were sent under it. |
| `status`, `waiting`, `error` | The newest turn's status, or the task's for a run, and the words of its error through `audit.Scrub`. |
| `models`, `calls`, `errors`, `refusals` | From its usage records. |
| `tools` | Each tool call, from the audit entries that ended one: name, outcome and time. |
| `usage` | Its tokens and cost, the sum of its usage records, so it equals what the inspector and the usage report show for it. |
| `started`, `ended` | Its first turn's start, and its last turn's end once none is open. |

## Limits

Each `Limit` is a place the install may run out of room:

| Key | Used of | Scope | Watch level |
| --- | --- | --- | --- |
| `cap` | This month's spend on hosted and trusted models, of the monthly cap, both from `usagereport.Read` with the month, not computed again. | tenant | The cap's own warning threshold, `usage.cap_warn_percent`, so the notice and the cap's warning never disagree. |
| `memory_reserve` | The memory pass's spend this month, of its reserve, from the same report. | tenant | `operations.watch_reserve_percent` |
| `disk` | The used space of the volume that holds the data folder, of its size. | install | `operations.watch_disk_percent` |
| `database` | `keystone.db` and its write-ahead log, of `operations.database_room_mb`. | tenant | `operations.watch_database_percent` |
| `files` | Every file in the person's `files/` folder, of `operations.files_room_mb`. | tenant | `operations.watch_files_percent` |
| `artifacts` | Every file in `artifacts/`, of `operations.artifacts_room_mb`. | tenant | `operations.watch_artifacts_percent` |
| `run:<request id>` | What each background task or scheduled run under way has spent on models that are not local, of its own budget ([unattended-spend.md](unattended-spend.md)). | tenant | `operations.watch_run_budget_percent` |

`percent` is used of the limit, rounded down, and may pass 100; a limit of nothing that holds something is 100, and a cap of nothing is full, since it stops every paid call. `state` is `ok`, `watch` (at or past the watch level), `full` (at or past the limit) or `unknown` (with `problem`). `crossed` is a state of `watch` or `full`, or a monthly cap that does not read, which stops every paid call.

**The disk is the volume, not the data folder.** It is read from the operating system for the folder that holds the person's database: Windows' free space for the caller, or `statfs` elsewhere. The data folder's own total is never shown: with two people on an install, it would tell one the size of the other's folder. The volume's use counts everything on it and says nothing of any one person (decision [0460](decisions/0460-the-disk-is-the-volume-and-each-person-sees-their-own-folder.md)). The database's, files' and artifacts' sizes are the person's own, read through their store's handles.

## Trends

A `Trend` is how a limit has moved and where it is heading:

| Field | Meaning |
| --- | --- |
| `points` | The snapshots within `operations.trend_window`, oldest first, and the reading now as the last. The cap's and the reserve's reach back no further than the start of the month, when they start again. |
| `known` | There are two points or more at different times. |
| `per_day` | The slope of the line that fits the points best, by least squares, in the limit's unit a day. |
| `full_at` | When the use would reach the limit at that rate; absent when it is not growing, is full, or would take more than a hundred years. |
| `resets`, `at_reset` | For the cap and the reserve, the first of next month, and what would be used by then at that rate: "at this rate, $51 by the end of the month". |

A run's budget has no snapshots: its points are its usage records on models that are not local, added up in order, and the reading now (decision [0461](decisions/0461-a-limits-trend-is-a-least-squares-line-through-hourly-snapshots.md)).

**Snapshots.** `operations_snapshots`, in each tenant's own database (migration `20261126T001900_operations.sql`): `at`, `key`, `used`, `of`, keyed by the limit and the time. The disk is kept in each person's database too, so nothing is kept outside a tenant's folder. The sampler, started by `internal/app` under the turn runner after the scheduler, samples every tenant at once and then every `operations.snapshot_every`; a tenant whose newest snapshot is younger than that is skipped, so a restart takes no second one within the interval. Each sample removes the snapshots older than `operations.snapshot_keep`. A limit that does not read is not recorded.

## The routes

| Route | Reply |
| --- | --- |
| `GET /api/v1/operations` | `Report`: `at`, `jobs`, `schedules`, `models`, `traces`, `limits`, `crossed`. |
| `GET /api/v1/operations/watch` | `WatchReply`: `at`, `crossed`, the limits at or past their watch level. |

Both added in `internal/app/app.go` with one line, and in `docs/api/openapi.json`. A report that cannot be read is 500 `failed`, with the reason in the server's log. The two routes are walked with every other by `tenant isolation`, `calls recorded` and `no keys in output` ([tools.md](tools.md)).

## Settings

In `settings/core.json`. A value outside its bounds is taken as the nearest bound, so a report is never refused for a setting.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `operations.snapshot_every` | `1h` | install | How often every person's limits are recorded. 1m to 24h. |
| `operations.snapshot_keep` | `840h` | install | How long a snapshot is kept: 35 days. |
| `operations.database_room_mb` | `2048` | install | The size a person's database is planned to grow to. |
| `operations.files_room_mb` | `20480` | install | The room for one person's files folder. |
| `operations.artifacts_room_mb` | `5120` | install | The room for one person's artifacts folder. |
| `operations.trend_window` | `168h` | tenant | How far back a trend looks. 1h to 90 days. |
| `operations.health_window` | `24h` | tenant | The window of model health and of failed tasks. 5m to 30 days. |
| `operations.jobs_shown` | `10` | tenant | How many of the newest tasks, and of the failed, are listed. 1 to 100. |
| `operations.traces_shown` | `20` | tenant | How many traces are listed. 1 to 100. |
| `operations.stuck_after` | `30m` | tenant | When an open round counts as stuck. |
| `operations.model_error_watch_percent` | `10` | tenant | The share of failed calls at which a model's health reads as errors. |
| `operations.watch_disk_percent` | `90` | tenant | The disk's watch level. |
| `operations.watch_database_percent` | `80` | tenant | The database's. |
| `operations.watch_files_percent` | `80` | tenant | The files folder's. |
| `operations.watch_artifacts_percent` | `80` | tenant | The artifacts folder's. |
| `operations.watch_reserve_percent` | `80` | tenant | The memory reserve's. |
| `operations.watch_run_budget_percent` | `80` | tenant | Each run's budget's. |

The cap's watch level is `usage.cap_warn_percent` ([usage.md](usage.md)). The person's settings have controls on Settings → General, drawn from the registry like every other ([settings-screens.md](settings-screens.md)). `app.ReadConfig` reads the two install settings of the sampler through `operations.ReadConfig`.

## Checked by

`go test ./internal/operations`.

- The item's proof, first half, `TestEachLimitHasItsShareAndTrend`: a week of hourly snapshots generated behind where each limit stands, growing at a known rate, with a snapshot from before the window; each of the six limits then has its share (used of its limit), its watch level, a trend whose last point is the reading now and whose first is within the window, a growth a day within 2% of the generated rate (10% for the disk, which the machine shares), a fill date that matches it, and for the cap and the reserve the first of December and what the rate reaches by then.
- The item's proof, second half, in the package, `TestACrossedWatchLevelIsWatched`: with a megabyte of room for files, 900 KB is watched at 87%, not at a level the person raised to 90, and 1.1 MB is full whatever the level; `TestTheRoutes`: the watch route the home screen reads has the cap at 90% for a person who spent $4.50 of $5, and nothing for another.
- `TestTheCapIsTheUsageReports`: the cap's spend, limit and level are `usagereport.Read`'s; at 82% it is watched, past its limit full, and a cap that does not read is crossed.
- `TestModelHealth`: calls by outcome, the median of the calls sent, the cost, the state of each kind (errors, failing, refused, idle), a model that left the registry, `where` with the host only, and a last error whose invented key is scrubbed.
- `TestJobsAndRunBudgets`: counts by state with paused and waiting, the newest, a stuck task and not a fresh, paused or waiting one, the failed within the window with its key scrubbed; each open task's budget a limit, one at 85% watched, with a trend from its usage records.
- `TestTraces`: three traces newest first (a task's rounds and note as one, a failed turn, a chat turn with tool steps), the inspector's chat and turn, models, calls, errors, refusals, tool steps in order, tokens and cost equal to the usage records, no key and nothing of another person's, and as many as the setting says.
- `TestSampleDue`, `TestTrend`, `TestSettingsAreHeldToTheirBounds`, and `TestTheRoutes`: each person sees their own report, the route's limits are `Read`'s, no key in either reply, and no session is refused.

Two faults were put in by hand and caught: a watch level that only a full limit crossed (by the unit tests and the standing check) and a trace that summed the wrong tokens.

In `verify`: the standing check `operations`, in `tools/verify/operations.go`. On the real server with a throwaway data folder: the server records the test tenant's limits when it starts; the test tenant sends a message; a week of snapshots is generated and the admin lowers the cap to a cent, of which nine tenths are spent; `GET /api/v1/operations` has every limit with its share, its watch level and a trend of more than a day ending at the reading now, the cap equal to `GET /api/v1/usage`'s, and the turn as a trace whose tokens and cost are its usage records'; `GET /api/v1/operations/watch` has the cap crossed; and a second tenant's report and watch hold none of the test tenant's chat, trace or spend.

## Not here yet

- **An admin's view of every person.** There is no admin role. Each person sees their own, and the disk as an install fact.
- **Buttons on what needs attention**: try again, reconnect Google, open models. The rows link to the chat, the Scheduled screen or Settings.
- **Past-chat search time and first words** as in the mockup: neither is recorded yet.
- **Turns by hour** and **cache hits** as in the mockup: both could be summed from the usage records.
- **A limit for the audit log's size alone.** It is part of the database, which is a limit.
