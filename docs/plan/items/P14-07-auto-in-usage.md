# P14-07. Auto in the usage report

Status: provisional
Lane: core + interface
Depends on: P14-04
Owner gate: Approval of the settings mockups (P1-07), for the screen only.
Built without the gate: The server side in full; the card from the draft, provisional.
Scope: The usage report's Auto figures: this month on Auto against the same tokens priced at the ladder's top step, and how often a stronger model was asked for. The Auto card on Usage.
Proof: The figures equal sums over the usage records and the verdicts of the test tenant.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Screens", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `core/p14-07-auto-in-usage`, cut from `reboot` at `165ada6`. Contract first: the report only gains a field, so every reader of `GET /api/v1/usage` and `usagereport.Read` (Operations, the load test, the usage ring, P15-02) keeps what it reads.

1. **Contract.** `usagereport.Report` gains `auto` (`usagereport.Auto`), and `docs/api/openapi.json` is regenerated. `Auto` holds, for the report's period:
   - `turns`: the messages routed on Auto, the distinct turns of `router_verdicts` whose first verdict falls in the period; `stronger_asked`, those of them whose verdict is marked `stronger_asked`; `rewrites`, those that were a stronger model's rewrite (`rules_only_why` is `stronger_asked`).
   - `replies`: the usage records of kind `chat` under a request id that has a verdict, summed as every line of the report is (`Sum`, split by class); `decisions`: the records of kind `decision` under such a request id; `cost_micros`, the two together.
   - `top_step`: the ladder's top step as it is set now (the highest name on `router.ladder` that is a chat model in the person's registry), its label and class, and `cost_micros`, the replies' same token counts priced at its price, call by call, as the turn prices a call (a refused call costs nothing); `problem` in words when there is no ladder or no step of it is in the registry.
   - `saved_micros`: the top step's cost less `cost_micros`, which may be below zero.
2. **Store.** One migration, `20261128T150000_auto_in_usage.sql`: an index on `router_verdicts(request_id)`, so the routed request ids of a period are found by a join from that period's usage records.
3. **Code.** New file `internal/usagereport/auto.go` (`ReadAuto`, `Auto`, `TopStep`); `Read` calls it on the records it already read (a few added lines in `read.go`). `tools/tenant usage` prints the Auto figures after the cap (added lines).
4. **Proof.** `go test ./internal/usagereport`: on generated records and verdicts over two months, with turns on Auto and off, rewrites and marked verdicts, decision calls, titles under routed request ids, refused calls and a verdict before the period for a call inside it, every Auto figure equals the records and verdicts summed by hand, for October, November and a range; the top step is the highest registered chat step, and each problem. `go test ./internal/turn`: the test tenant's recorded ladder, messages on Auto and off, two stronger models asked for, and the report's Auto figures equal sums taken straight from its `usage_records` and `router_verdicts` tables, with its top step priced from a registry entry added for the test.
5. **Screen**, provisional on the settings mockups (P1-07): an Auto card on Settings → Usage after the totals (new `UsageAuto.tsx` and `usage-auto.css`; one import and one element in `UsageSettings.tsx`, one field in `usageCalls.ts`): what the replies on Auto cost with their decisions against the same tokens at the top step, a bar, what was saved, and how often a stronger model was asked for. Hidden when the period has nothing on Auto.

No setting: the report reads `router.ladder` as it stands, as it reads today's cap for a past month. Left to others: the admin's per-person figures (P15-02 reads the same report).

## Result

Provisional on November 28, 2026, on the settings mockups (P1-07) for the card only (journal `docs/journal/2026-11-28-core-020.md`). The server side is done: the usage report's `auto` field (`usagereport.ReadAuto`, called by `Read`), with the replies on Auto and the router's decisions summed from the usage records under a request id that has a verdict, the same tokens priced call by call at the ladder's top step as it stands now, what was saved, and the turns, stronger asks and rewrites counted from `router_verdicts`; an index on `router_verdicts(request_id)`; and the paragraph in `tools/tenant usage`. The proof is `internal/turn/autousage_test.go` on the test tenant and `internal/usagereport/auto_test.go` on generated data. The Auto card on Settings → Usage type-checks and builds and has not been seen in a browser. Decisions 0740 to 0743. Surface: `usagereport.Report.Auto` (JSON `auto`), `usagereport.ReadAuto`, `usagereport.Auto`, `usagereport.TopStep`.
