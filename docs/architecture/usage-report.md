# The usage report

Folders: `internal/usagereport`, `tools/tenant/usage.go`, and the screen in `web/app/src/` (`UsageSettings.tsx`, `usageCalls.ts`, `usage.css`)

What a person's model calls cost and their tokens, for a period: in total, by model, by chat and by kind of work, each split into local, hosted and trusted models, with the calendar month against the monthly cap and the memory pass's reserve. Built in P11-02. The screen is provisional: see "Provisional" at the end.

The report reads the usage records ([usage.md](usage.md)) and nothing else that keeps a total, so it cannot drift from them. Every breakdown is summed from the same records as the total, each record lands on exactly one line of each, and so the lines of each breakdown add up to the total, to the millionth of a dollar. The package writes nothing, and nothing about how a record is written changed.

## One function

```go
rep, err := usagereport.Read(ctx, store, registry, usagereport.Ask{Month: "2026-11"}, time.Now())
```

`Read` is the one function behind `GET /api/v1/usage` and `tools/tenant usage`, so the screen and the command line cannot disagree. It reads the person's settings, works out the period, reads the period's records with `usage.List`, sums them, and reads where the month stands against the cap and the reserve.

| Exported | What it is |
| --- | --- |
| `Read(ctx, store, reg, ask, now)` | The report for one tenant. An ask that cannot be read wraps `ErrPeriod`. |
| `Ask` | `Period` (a preset's name), `Month` (2026-11), `From` and `To` (days), `Rows`. The zero `Ask` is the person's default period and rows. |
| `Resolve(ask, preset, maxDays, now)` | The days an ask covers, as a `Period`. |
| `Records(ctx, store, period)` | The period's usage records, oldest first. |
| `Summarise(ctx, store, records, rows)` | Records summed into `Breakdowns`: the total and the three breakdowns. |
| `ReadConfig(ctx, store, reg)` | The report's own three settings, as they stand for the tenant. |
| `Routes(Options)` | The route. `Options` holds `PageLimit`, the log and a clock. |
| `Presets()`, `WorkLabel(kind)` | The named periods; a kind of work in words. |
| `Sum`, `Row`, `Breakdowns`, `Cap`, `Reserve`, `Report`, `Period` | The shapes, below. |

The package imports the usage records, the tenant store, tenant settings and the settings registry, the model registry (for labels), the audit log (to read where an API call went), the memory pass's reserve (to read it as the memory pass does) and the API shell for its route. It enforces no fixed rule. A person reaches only their own tenant because the route acts on the store the shell hands it (fixed rule 1).

## The period

Days are calendar days in UTC, as the cap's month is (decision [0162](decisions/0162-the-cap-counts-a-utc-calendar-month.md)).

| Asked for | Covers |
| --- | --- |
| nothing | The person's `usage.report_period`. |
| `period=this_month` | This calendar month, the whole of it. |
| `period=last_month` | The calendar month before. |
| `period=last_7_days`, `last_30_days` | That many days, today the last. |
| `month=2026-11` | That calendar month. |
| `from=2026-10-31&to=2026-11-01` | The days from the first to the last, both included, at most `usage.report_max_days` of them. |

Only one of the three may be given. A period's `month` is set when it is exactly one calendar month, a range that happens to be one included.

## The breakdowns

`Sum` is calls, input, output, cache read and cache write tokens, and cost in millionths of a US dollar (`usage.Total`), and the same split into `local`, `hosted` and `trusted`. A local model costs nothing, and its calls and tokens are counted all the same. `Sum.Capped()` is the hosted and trusted cost, what counts against the cap. A refused call, one the cap stopped, is a call that cost nothing, as its record says.

| Breakdown | A line is | Key and label |
| --- | --- | --- |
| `models` | One model and its class when the calls were made. A model whose class changed has a line for each, as in `usage.Month`. | Its registry name; its label in the registry, or its name once it has left. |
| `chats` | One chat the person still has, or the calls in no chat they still have. | The chat's id and title ("Untitled chat" when it has none); an empty key and "Not in a chat you still have". |
| `kinds` | One kind of work. | `chat`, `title`, `summary`, `memory`, `embedding`, any later kind as it is, `background_task` or `scheduled_task`, with words: "Chat replies", "Summaries and recaps", and so on. |

Lines are costliest first, then by calls, label and key. A breakdown shows `rows` lines, the person's `usage.report_rows` unless the ask says, and adds up the rest in one more line, with an empty key, a `rest` count and a label such as "4 more chats". So a breakdown still adds up to the total when it is cut short.

### Where a record goes

A usage record names no chat and no task. It carries the request id of the API call that set it off, which every model call made for that call carries too. The report finds where a request id's calls belong from what the tenant's database still holds, in this order (decision [0430](decisions/0430-a-usage-record-is-placed-by-its-request-id.md)):

1. **A background task** whose own request id it is (`jobs.request_id`). The kind of work is `background_task`, or `scheduled_task` when a schedule run started the task (`schedule_runs.job_id`), whatever each call's own kind. The chat is the task's work chat, not the chat its note is written in, though the note's turn carries the same request id. This is how the task's budget counts its calls too ([jobs.md](jobs.md)).
2. **A turn** sent under it (`turns.request_id`): the turn's chat. A reply, the chat's title, a summary of its oldest turns, a recap and the memory pass are all made under the turn's request id, and so are a background task's turns.
3. **The API call** in the audit log, when its path is a chat's or under one (`POST /api/v1/chats/{id}/recap`): that chat. A recap asked for through the recap route has no turn.

A chat found that way that the tenant no longer has, or no chat at all, puts the calls on the line "Not in a chat you still have": a deleted chat's turns go with it, and its calls stay in the usage records because the money was spent. Outside a task, a record's kind of work is its own kind.

Compaction and recaps both write the kind `summary`, so they share a line, "Summaries and recaps". Nothing in a record tells them apart, and the record is not changed to.

A migration, `20261125T222300_usage_report.sql`, adds the index `turns_by_request` on `turns(request_id, seq)`, so step 2 is a lookup. Each request id is placed once per report.

## The cap and the reserve

`cap` is about a calendar month: the period's when it is one, and the month of now otherwise.

| Field | Meaning |
| --- | --- |
| `month` | The calendar month in UTC. |
| `spent_micros` | Its cost on models that are not local, from `usage.Spent`, which is what the gate reads before a paid call. For a period that is the month, it equals the report's own `total.hosted` and `total.trusted` cost, and a test and the check prove it. |
| `limit_micros`, `warn_percent`, `warn_at_micros` | `usage.monthly_cap_usd` and `usage.cap_warn_percent` as they are set now, read as the turn reads them, and the threshold `usage.Judge` works out. A past month is shown against today's cap: settings keep no history. |
| `left_micros` | What may still be spent, never less than nothing. |
| `state` | `ok`; `warning`, at or past the threshold; `reached`, at or past the cap, so a call to a model that is not local is stopped; or `unread`. |
| `problem` | Why the cap does not read, when `state` is `unread`. A value that is not a number is not in force, so tenant settings fall back to the default; a number that is not a cap, such as a negative one, stops every paid call at the gate, and the report says so instead of failing. |

`memory_reserve` is the memory pass's reserve, `knowledge.memory_reserve_usd`, a part of the cap (decision [0280](decisions/0280-the-memory-reserve-is-a-part-of-the-cap.md)): `limit_micros`, the pass's spend in the same month on models that are not local (`usage.SpentOn(month, "memory")`, as the memory pass reads it), what is left, and a `problem` when it does not read.

## The route

`GET /api/v1/usage`, added in `internal/app/app.go` with one line. Query: `period`, `month`, `from` and `to`, and `rows`, from 1 to `server.page_limit`. The answer is the `Report` as JSON: `period`, `total`, `models`, `chats`, `kinds`, `rows`, `cap` and `memory_reserve`. `docs/api/openapi.json` has every shape.

| Code | Status | Meaning |
| --- | --- | --- |
| `bad_period` | 400 | The period cannot be read: not a preset, not a month, a range backwards or too long, or more than one kind of period. |
| `bad_request` | 400 | `rows` is not a whole number from 1 to `server.page_limit`. |
| `failed` | 500 | The report could not be read. The server's log says why. |

## From the command line

    go run ./tools/tenant usage <tenant-id>
    go run ./tools/tenant usage <tenant-id> 2026-10
    go run ./tools/tenant usage <tenant-id> last_30_days
    go run ./tools/tenant usage <tenant-id> 2026-10-25 2026-11-05

It prints the period, the tables by model, by kind of work and by chat, the split by class, where the month stands against the cap, and the memory pass's reserve. It calls `Read`, so it prints what the screen shows.

## The screen

Settings → Usage, at `/settings/usage`, in `web/app/src/UsageSettings.tsx` with `usage.css`; its one call is in `usageCalls.ts`, kept apart from `api.ts` as the Memory screen's are. It took the place of the Spend placeholder, and `/settings/spend` goes to it.

- **The month against the cap.** A card after the draft spend card of `design/screens/Operations.html`: "Spend this month" (or "in October 2026" for a past month), a pill (On track, Near the cap, Cap reached, Cap does not read), "$41.20 of $100.00" in the serif, and a bar with a tick at the warning threshold. Below: what is left and where the warning starts, or that calls to hosted and trusted models are stopped; that the cap counts hosted and trusted models, that the admin sets it, and that the month is a calendar month in UTC. Under a rule, what the memory pass spent of its reserve, with a small bar.
- **The period picker.** This month, last month, the last 7 or 30 days, a month (a month field), or dates (two date fields and Show). It opens on the person's `usage.report_period`, as the server answers with no query. A period the server refuses is shown in its words, and the last report stays.
- **The totals.** The period in words ("31 October to 1 November 2026"), then cost, calls, tokens in and out, read from and written to the cache, and the split: "Hosted $1.50 · trusted $0.23 · local 24 calls, free".
- **Three tables**, by model, by chat and by kind of work, costliest first as the server sends them: calls, in, out, cache read, cache write and cost, a total row, and under each name a small bar of its cost against the costliest line and its split by class. A chat's name links to the chat. The line of calls in no chat and the line that adds up the rest are in the quiet colour and link nowhere. A table wider than a phone scrolls inside its card.

The bars are inline SVG: rectangles whose width is an attribute, filled from the tokens (`--ok`, `--accent`, `--err` on `--sunk`), so the page has no `style` attribute, which the content security policy would refuse, and no chart library (decision [0432](decisions/0432-the-usage-bars-are-inline-svg.md)). The cap's bar is an image with a sentence for a screen reader; the small bars are hidden from it, since the numbers beside them say the same.

## Settings

In `settings/core.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `usage.report_period` | `this_month` | tenant | The period shown when none is asked for: `this_month`, `last_month`, `last_7_days` or `last_30_days`. |
| `usage.report_rows` | `10` | tenant | How many lines each breakdown shows before the rest are added up in one more. Below 1 is taken as 1. |
| `usage.report_max_days` | `366` | install | The most days one report may cover when it is asked for a range. |

The two tenant settings have controls on Settings → General, drawn from the registry like every other ([settings-screens.md](settings-screens.md)). The cap, its threshold and the reserve are read here and set where they always were: `settings/models.json` and `settings/knowledge.json`.

## Checked by

`go test ./internal/usagereport ./tools/tenant`.

- The item's proof, `TestTotalsEqualTheSumOfTheRecords`: on 900 generated records over five models of the three classes, five chats one of them untitled, a background task, a scheduled task, a recap asked for through the API, a deleted chat, calls under an API call that names no chat and under no request id, eight kinds of work, refused calls at no cost, and times from 25 October to 6 November 2026 with records on each side of midnight UTC on 1 November and at the first moment after a range: for October, November, this month, last month, the last 7 days, a range of two days across the boundary and a range of three months, the total equals the records summed by hand, every breakdown's lines add up to the total field by field and class by class, every line is exactly the records that belong to it, and the cap's spend is the month's cost on models that are not local.
- `TestRecordsArePlaced`: a task's calls go under its work chat and not the chat of its note, a scheduled task's under its run's chat, a recap asked for through the API under its chat, a deleted chat's under the line of calls in no chat; labels come from titles and the registry.
- `TestTheRestAreAddedUp`: with 1, 2, 3, 4 and 100 rows, the lines still add up, the last line adds up the right number, and lines are costliest first.
- `TestTheCapAndTheReserve`: the shipped cap and reserve, the warning, the cap reached, a lowered cap with nothing left, and a cap and a reserve that do not read.
- `TestAReportIsOneTenants`, `TestReadingChangesNothing`, `TestResolve`, `TestThePresetsAreTheSettingsOptions`, `TestChatInPath`.
- `TestTheRoute`: the route gives byte for byte what `Read` gives, each person sees only their own usage, no session is refused, and a bad period or number of rows is refused with its code.
- In `tools/tenant`, `TestUsagePrintsAMonthAndTheCap`: a month, a range, a named period, and the refusals.

Three faults were put in by hand and each was caught: a task's calls not placed under the task, the rest of a breakdown not added up, and a range that kept its first moment after.

In `verify`: the standing check `usage report`, in `tools/verify/usagereport.go`. On the real server with a throwaway data folder, the test tenant sends messages in two chats, then 240 records for invented models of each class and five kinds of work are added under those turns' request ids and under none, from 30 October to 2 November 2026 with one on each side of midnight UTC. For October, November, a range across the boundary, November with one row, and this month, `GET /api/v1/usage` is compared with sums taken straight from the `usage_records` table: the total and its split by class equal the table's, each breakdown's lines add up to the total, the cap's spend is the table's cost on models that are not local in the cap's month, the cap is the setting's, and each chat line names a chat of the tenant's by its title. A second tenant's report holds its own one call. The route is walked with every other by `tenant isolation`, `calls recorded` and `no keys in output` ([tools.md](tools.md)).

## Not here yet

- **Unattended spend.** The per-run budgets of background tasks, schedules and sub-agents (P8-05) read the same records. The report counts a task's calls under its kind of work and does not show a run against its budget.
- **Operations.** The spend line on Operations and Monitor (P11-03) can call `Read` with the month.
- **A chart over time.** The report is totals for a period. Spend by day would be one more breakdown over the same records.
- **An admin's view of every person.** There is no admin role; each person sees their own.

## Provisional

- **The owner gate.** The screen waits on the settings mockups (P1-07), which are not approved; there is no settings mockup at all. The cap card follows the spend card of `Operations.html`, a draft in the first round's colours, and the tables follow its tables; the colours are the tokens. The period picker, the totals, the words and the order of the sections were chosen without a mockup.
- **Not seen in a browser.** The screen type-checks and builds (`npm run build`), and on a server started from this branch with a data folder of its own the sign-in page loaded from the new build and `GET /api/v1/usage` answered the report the screen reads. Signing in to look at the screen itself was left to a person: see the journal, core 011.
- **Owner answers it builds on.** The cap's month is a calendar month in UTC, and the memory reserve is a part of the cap, as `STATE.md` recommends; a local model is outside the cap, and a home-network model that forwards to a paid provider is under it only when marked trusted. If any answer changes, the report follows the gates, since it reads the same functions.
