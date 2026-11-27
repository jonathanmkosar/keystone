# Usage and the spend cap

Folder: `internal/usage`

Every model call leaves one usage record in the tenant's own database. The records are what the month's spend is summed from, and the monthly cap reads that sum before a call that costs money is sent.

The package reads no setting. The cap and its threshold are arguments, and the turn runner reads them from settings (`internal/turn/route.go`).

## A record

| Field | Meaning |
| --- | --- |
| `at` | When the call ended, in UTC. |
| `request_id` | The id of the API call that set it off, the same one its audit entries carry. |
| `kind` | The kind of task: `chat`, `title` and the others in [routing.md](routing.md). |
| `model`, `class` | The registry name of the model that took the call, and its class at that moment. |
| `outcome` | `ok`, `stopped` (the person stopped it), `refused` (Keystone did not send it) or `error`. |
| tokens | Input, output, cache read, cache write and thinking, as the seam counts them, and whether they were estimated. |
| `cost_micros` | The cost in millionths of a US dollar, from `Price.CostMicros` on the model that answered. |
| `duration_ms` | How long the call took. |

The table is `usage_records`, added by the migration `20261125T050000_usage.sql`. Rows are added and read and nothing else: triggers refuse a change, a delete and a replace, as in the audit log. Deleting a chat leaves its calls here, because the money was spent either way.

The audit log holds what was sent and what came back ([audit.md](audit.md)). The usage records hold the numbers, by month, without the content. One is not built from the other: the turn writes both.

## Who writes one

`Runner.call` in `internal/turn` writes a record after the audit entry for the call's end, for every call that ended: a whole reply, a stopped one, a refused one and a failed one. A model of a route that did not answer, before the next one was tried, gets its record from the chain's `Moved` hook.

A call the server was stopped during has a started entry in the audit log and no usage record, since it never ended here.

Since P5-02, a summary of a chat's oldest turns is a call like these, made beside the turn under its request id, and its record has the kind `summary` ([compaction.md](compaction.md)).

Since P5-03 the cache counts come from every adapter whose provider reports them: the Anthropic adapter's reads and writes, and the reads of the Responses and Chat Completions adapters. A turn that reads the start of its chat from the cache costs its reads at the entry's `cache_read` price, proven end to end against a stand-in Anthropic server ([caching.md](caching.md)).

Since P6-06, the memory pass's call is one too, made after the turn under its request id, with the kind `memory`. `usage.SpentOn(ctx, store, month, kind)` sums one kind of task on models that are not local, which is what the memory pass's reserve, a part of the monthly cap, is measured against ([memory-pass.md](memory-pass.md)).

Since P6-08, every embedding call is one too, with the kind `embedding`: the query recall makes a vector of, under the turn's request id, and the background refresh's, under a request id of its own (`embeddings-<hex>`) or the route's. Only input tokens are counted, at the entry's input price, estimated when the endpoint does not say. The embedding reserve, `knowledge.embedding_reserve_usd`, is measured with `SpentOn(month, "embedding")` as the memory pass's is ([embeddings.md](embeddings.md)).

## Reading them

```go
sum, err := usage.Month(ctx, store, "2026-11")   // totals, and a line for each model
spent, err := usage.Spent(ctx, store, "2026-11") // what counts against the cap
records, err := usage.List(ctx, store, usage.Filter{Month: "2026-11", Limit: 50})
```

From the command line:

    go run ./tools/tenant usage <tenant-id>
    go run ./tools/tenant usage <tenant-id> 2026-10

It prints the month by model and where the month stands against the cap.

`List` puts only the conditions that are set into its query, so a month's records and a request's records are found by their index (`usage_records_month`, `usage_records_request`). Until the load test (P13-03) it sent one query for every filter, `(? = '' OR month = ?) AND (? = '' OR request_id = ?)`, which SQLite can only answer by reading every record a person has: 125 milliseconds for one request's records among 55,000, and the Operations report asks once for each of its twenty traces. `TestListFindsAMonthAndARequestByTheirIndex` reads the query's plan and fails if it scans the table.

A month is a calendar month in UTC. Decision [0162](decisions/0162-the-cap-counts-a-utc-calendar-month.md).

## The monthly cap

The plan: hosted spend defaults to a $100 monthly cap per tenant, with a warning near it and a stop at it.

- **What counts.** The cost of calls to models whose class is not `local`: `hosted` and `trusted`. A local model is never asked about and never stopped.
- **The stop.** Before a call is sent, the turn works out the most it could cost: its estimated input with the budget's margin, at the dearer of the input price and the cache-write price, and the whole room kept for the reply at the output price. If the month's spend and that amount are together more than the cap, the call is not sent. So the month cannot end over the cap. Decision [0161](decisions/0161-the-cap-stops-a-call-that-could-pass-it.md).
- **The warning.** When the spend and the next call together reach `usage.cap_warn_percent` of the cap, the answer is `warning`. The call is made.
- **A cap that cannot be read stops the call.** A stored value that is not a number, or a sum that fails, never lets a paid call through.

`usage.Judge(spent, next, cap)` is the rule with no store. `usage.Allow(ctx, store, cap, at, next)` sums the month and asks it.

### What a stopped call looks like

The cap is the chain's gate ([routing.md](routing.md)). A stopped call is a model call that failed with the kind `spend_cap`, a kind the seam has for this and no provider gives.

- The turn fails with the code `spend_cap`, and a message that gives the month's spend, the cap and what the call could have cost.
- The audit log has the call's started entry and an end entry with the outcome `refused`.
- The usage record has the outcome `refused` and a cost of nothing.
- If the person's fallback chain holds a local model, the call goes to it. Decision [0160](decisions/0160-a-capped-call-falls-back-to-a-local-model.md).

## Settings

In `settings/models.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `usage.monthly_cap_usd` | `100` | tenant-admin | The most one person may spend in a calendar month on models that are not local. Zero stops every call that costs anything. |
| `usage.cap_warn_percent` | `80` | tenant-admin | The share of the cap at which the warning starts. |

Both are set by the admin for a tenant, so a person cannot raise their own cap.

## Checked by

`go test ./internal/usage ./internal/turn ./tools/tenant`.

- A month sums by model, counts only models that are not local against the cap, and is one tenant's own.
- A record with no kind, no model, an unknown class or outcome, or a negative number is refused. A row cannot be changed, deleted or replaced, and its month must be its time's.
- The cap's three answers at each edge: under the threshold, at it, at the cap exactly, and one millionth over.
- In a turn: a call under the cap runs and costs what its tokens say, a call over it is stopped and never reaches the provider, a local model is not held, and a cap that does not read stops a paid call.

In `verify`: the standing check `spend cap`, in `tools/verify/spendcap.go`. It runs the real server on a throwaway data folder with the fake provider behind a model marked hosted. A message under the cap is answered; after the admin lowers the cap the next is stopped with `spend_cap` and the provider is not called; and for that tenant and the test tenant, every model call the audit log says ended has a usage record under the same request id ([tools.md](tools.md)).

## Not here yet

- **A route and a screen.** Since P11-02, `GET /api/v1/usage`, Settings → Usage and `tools/tenant usage` show a period's records by model, by chat and by kind of work against the cap, from one function, `usagereport.Read` ([usage-report.md](usage-report.md)). The spend line on Operations is P11-03.
  - Built in P11-03 ([operations.md](operations.md)): Operations reads each model's calls, errors, refusals and median time from the records over a window, and each trace's tokens and cost.
- **The warning in front of the person.** It is in the server's log and in `tools/tenant usage`. Nothing in the turn's stream carries it yet.
- **Unattended work.** Each background run, schedule and sub-agent gets its own budget in P8-05. The cap's gate already stops or falls back any call that goes through a chain.
  - Since P8-01 a background task has a budget of its own, `agents.task_budget_usd`, summed from the usage records of its own request id and asked after the cap through `turn.Options.Gate` ([jobs.md](jobs.md)).
  - Since P8-05 every unattended run (a task, a scheduled run, a sub-agent) has its budget in a run registry, and at the cap or its budget it goes on with a local model in the chain and its chat is told which model wrote the reply, or stops when `agents.unattended_fallback` is off, through `turn.Options.Moved` ([unattended-spend.md](unattended-spend.md)). A chat at the cap is unchanged.
- **Two calls at the same moment.** Each reads the month before it is sent, so two chats answering at once can together pass the cap by one call's cost.
- **Cost of a call that broke off.** A provider reports usage at the end of a reply, so a stopped or broken call is recorded with no tokens and no cost, though the provider may bill for what it wrote.
- **Prices change.** A record keeps the cost worked out when the call was made, from the entry's price that day.
