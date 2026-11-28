# Auto in the usage report

Folders: `internal/usagereport` (`auto.go`), `internal/tenant/migrations/20261128T150000_auto_in_usage.sql`, `tools/tenant/usageauto.go`, and the card in `web/app/src/` (`UsageAuto.tsx`, `usage-auto.css`)

What Auto cost a person in a period, against what the same tokens would have cost at the top step of their ladder, and how often they asked for a stronger model. Built in P14-07, from `docs/plan/SPEC-router-and-admin.md`, Part 1, "Screens": "Usage: an Auto card". The card is provisional: see "Provisional" at the end.

The figures are a field of the usage report ([usage-report.md](usage-report.md)), `auto`, summed each time from the same usage records as the rest of the report and from the router's verdicts (`router_verdicts`, [router.md](router.md) and [turns.md](turns.md)). Nothing keeps a total of its own, and nothing is written. Every other field of the report is as it was, so a reader that does not know `auto` reads what it read before.

## One function

```go
a, err := usagereport.ReadAuto(ctx, store, registry, records, period)
```

`Read` calls it on the records of its report, so `GET /api/v1/usage`, `tools/tenant usage` and the admin's reading of a person's report (P15-02) carry the same figures. `ReadAuto` is exported for a caller that has a period's records already.

| Exported | What it is |
| --- | --- |
| `ReadAuto(ctx, store, reg, records, period)` | The Auto figures of a period, from its records (as `Records` reads them) and the tenant's verdicts. |
| `Auto` | `turns`, `stronger_asked`, `rewrites`, `replies`, `decisions`, `cost_micros`, `top_step`, `saved_micros`. |
| `TopStep` | `name`, `label`, `class`, `cost_micros`, `problem`. |
| `Report.Auto` | The report's new field, `auto`. |

## What counts as Auto

A message on Auto is routed before its call, and each routing writes a row of `router_verdicts` with the turn's request id. A usage record carries the request id of the API call that set it off (decision [0430](decisions/0430-a-usage-record-is-placed-by-its-request-id.md)). So (decision [0741](decisions/0741-autos-calls-are-the-replies-and-decisions-under-a-routed-request-id.md)):

| Field | Is |
| --- | --- |
| `replies` | The period's records of kind `chat` under a request id that has a verdict: every call of a reply on Auto, its tool steps and a call the chain moved to included. Summed as every line of the report is (`Sum`: calls, tokens, cost, split into local, hosted and trusted). |
| `decisions` | The period's records of kind `decision` under such a request id: the router's verdict calls. A local decision model costs nothing. |
| `cost_micros` | `replies` and `decisions` together: what Auto cost. |

A chat's title, a summary, a recap or the memory pass made under the same request id is not Auto's: the router does not choose their model, and the report counts them under their own kind of work.

The records are the report's own, so a call is in the period of its own time, as everywhere in the report. The request ids are found by one join from the usage records of the period's months to their verdicts, through a new index, `router_verdicts_by_request` (migration `20261128T150000_auto_in_usage.sql`); a verdict written before the period, for a turn whose calls fall in it, counts all the same.

## The top step

`top_step` is the ladder's top step as `router.ladder` and the registry stand now: the highest name on the ladder that is a model in the person's registry, read without a problem, and takes chat calls (not an embedding or a decision model). The router skips a name that is not in the registry, so that is the step a message would reach at most (decision [0742](decisions/0742-the-top-step-is-the-ladders-highest-chat-model-now-at-its-price-today.md)).

- `cost_micros` is each reply call's own four token counts (input, output, cache read, cache write) at the top step's price, rounded to the millionth call by call, as the turn prices a call (`models.Price.CostMicros`), and added up. A refused call was never sent and costs nothing.
- `saved_micros` is the top step's cost less `cost_micros`, the decisions included. Below zero, Auto cost more than the top step would have.
- A past period is priced at today's ladder and today's price: settings keep no history, as the cap's do not.
- `problem`, in words, when there is no top step to price at: no ladder is set; the ladder does not read; or no step of it is a chat model in the registry. `cost_micros` and `saved_micros` are then zero; what Auto cost is still counted.

The test tenant's recorded steps are in no registry (decision 0695), so its top step has a problem unless an entry with the large step's name is added; the test in `internal/turn` adds one.

## The stronger model

From the verdicts, by turn (decision [0743](decisions/0743-how-often-a-stronger-model-was-asked-for-is-counted-by-turn-from-the-verdicts.md)):

| Field | Is |
| --- | --- |
| `turns` | The turns routed on Auto whose first verdict was written in the period. A turn routed again when its local step did not answer has two verdicts and is one turn. A stronger model's rewrite is a turn of its own. |
| `stronger_asked` | Those of them that any verdict marks `stronger_asked`: the person asked for a stronger model for that reply. |
| `rewrites` | Those of them that were a stronger model's rewrite of another (`rules_only_why` is `stronger_asked`). |

So "a stronger model was asked for on 3 of 120 replies" is `stronger_asked` of `turns`.

## The period

The report's period (decision [0740](decisions/0740-the-auto-figures-cover-the-reports-period.md)). Opened with no query it is the person's `usage.report_period`, this month as shipped, which is the spec's "this month on Auto"; another period picked on the screen moves the Auto card with the totals and the tables.

## From the command line

`go run ./tools/tenant usage <tenant-id> ...` prints, after the cap and the reserve, one paragraph when the period has anything on Auto:

    On Auto: 120 replies, whose 168 calls cost $3.20, and 118 decisions, which cost $0.00: $3.20 in all. The same tokens at the top step, Example Large (example-large), would have cost $12.80: $9.60 saved.
    A stronger model was asked for on 3 of the 120 replies; 3 of them were a stronger model's rewrite.

## The card

On Settings → Usage, after the totals, in `web/app/src/UsageAuto.tsx` with `usage-auto.css`; `UsageSettings.tsx` gained an import and the element, and `usageCalls.ts` the field `auto` with its types.

- **The head.** "On Auto this month" (or "in this period"), and a pill: "$9.60 saved", or "$0.40 more" when Auto cost more than the top step.
- **The amount.** What Auto cost, in the serif, "against $12.80 at the top step".
- **Two bars**, Auto and the top step by its label, each with its amount, scaled to the larger. An image with one sentence for a screen reader.
- **The sentence.** How many replies, their calls and cost, how many of them a local model answered at no cost, the router's decisions and their cost, and what the same tokens at the top step would have cost at its price today; or the top step's problem.
- **Under a rule**, how often a stronger model was asked for, as a share of the replies, and how many replies were written again a step up.

The card is hidden when the period has nothing on Auto. The bars are inline SVG with their widths as attributes, as the rest of the screen's (decision [0432](decisions/0432-the-usage-bars-are-inline-svg.md)).

## Settings

None of its own. It reads `router.ladder` (`settings/models.json`) as the router does, through the person's settings.

## Checked by

`go test ./internal/usagereport ./internal/turn ./tools/tenant`.

- The item's proof on the test tenant, `TestAutoInUsageIsTheTestTenantsRecordsAndVerdicts` (`internal/turn`): its recorded ladder answers three messages on Auto, a stronger model is asked for twice (the second time of the first rewrite), and a message on the model picked is not on Auto. The report's turns, stronger asks and rewrites equal counts of `router_verdicts`; its replies and decisions equal sums of `usage_records` under the verdicts' request ids; the top step's cost equals the table's calls priced by hand at the entry added for the large step; and what was saved is the difference.
- `TestAutoIsTheRecordsAndTheVerdicts`: on generated turns on Auto and off from 25 October to 6 November 2026, with verdicts and calls on each side of midnight UTC on 1 November, turns routed twice, rewrites, marked verdicts, refused calls with and without tokens, a model that left the registry, titles under routed request ids and a chat call under no request id, every Auto figure equals the records and verdicts summed by hand, with the top step priced by hand, for October, November, this month, a range across the boundary and the last 7 days; October's and November's turns and replies add up to the two months'.
- `TestTheTopStep`: the highest chat model of the ladder in the registry, past an embedding model, a decision model and a name in no registry; no ladder; a ladder of names in no registry; a ladder that does not read. What Auto cost does not move with the ladder.
- `TestNothingOnAuto`; in `tools/tenant`, `TestUsagePrintsAuto`.

In `verify`, through `tests`. The `usage report` check is unchanged and still compares the rest of the report with the table.

## Provisional

- **The owner gate.** The card waits on the settings mockups (P1-07); there is no draft of an Auto card, so it follows the spend card above it in the tokens, and its words and order were chosen without a mockup.
- **Not seen in a browser by a person.** See the journal, core 020, for what was run.
