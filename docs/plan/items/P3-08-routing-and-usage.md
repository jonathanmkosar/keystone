# P3-08. Routing, fallbacks, usage and the spend cap

Status: done
Lane: models
Depends on: P3-03, P3-04, P3-05, P3-06, P3-07
Owner gate: none
Scope: Rule routing by task kind; privacy routing (sensitive content to local or trusted only); fallback chains; a usage record per call; the monthly cap with warning and stop.
Proof: Every call has a usage record; a call over the cap is stopped; a failing endpoint falls back; the spend-cap check is added.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

The item is built in two parts, and the first is committed on its own because it unblocks the owner.

**Part A, the tenant model command.** `tools/tenant` gets `model add <tenant-id> <file.json|->`, `model list <tenant-id>` and `model remove <tenant-id> <name>`, over `models.Decode`, `models.Add`, `models.List` and `models.Remove`. An entry only names its secret, so no command can print a key. The runbook `docs/runbooks/adding-a-model.md` has one invented example for each real adapter.

**Part B, routing and usage.** Two new packages, and a join in the turn.

- `internal/routing` holds the rules and no state. `Plan(rules, ask, models)` gives the models one call may be sent to, in order: the model the task kind's rule names (chat uses the model picked; title, summary, memory and embedding each have a rule), then the fallback chain. When the ask is marked sensitive, a model for which `Model.MayReadSensitive()` is false is left out, and the route says what was withheld. `Chain` is a `seam.Provider` over the route's opened models. It tries each in order and moves on only while nothing of the reply has been passed on. It reads no setting: the rules, the sensitive mark and the gate are arguments. The package is added to the guards of fixed rule 3 in `tools/verify/fixedrules.go`.
- `internal/usage` holds one row for each model call in the tenant's own database (a new migration, `20261125T050000_usage.sql`): when, the task kind, the model and its class, the outcome, the tokens, the cost in micros and the time it took. `Month` sums a month for a tenant, by model. `Allow` is the monthly cap: it compares what was spent on models that are not local, plus the most the next call could cost, with the cap, and answers ok, warning or stopped.
- The turn (`internal/turn`) calls the two in one new file of its own, `route.go`. `open` asks for the route and gets the chain back, and `call` writes the usage record after the audit entry. The edits to existing lines are kept to those two places, because P5-01 is changing the same package.
- Settings in `settings/models.json`: the rule models, the fallback chain and its length, the monthly cap and the warning threshold.

Proof:

- Unit tests in both packages: rule and privacy routing, a failing endpoint falls back before any of the reply is passed on and does not after, the month's sums, and the cap's three answers.
- Tests in `internal/turn`: a turn on a failing model is answered by the next model in the chain, with both calls in the audit log and in the usage records.
- A new standing check, `spend cap`, in `tools/verify/spendcap.go`: with the fake provider behind a model marked hosted, a call under the cap runs, a call over it is stopped before the provider is called, and every model call in the audit log has a usage record.

### Result

Built as planned, with three additions.

- **The model command** is in `tools/tenant/model.go`, with the runbook `docs/runbooks/adding-a-model.md`. A tenant can be given a real model without code.
- **`internal/routing`**: `Plan` for the rules and the privacy filter, `Chain` for the fallback. A call falls back only while no block of its reply has been passed on, and each model is called once. The package is a guard for fixed rule 3 in `tools/verify/fixedrules.go`.
- **`internal/usage`**: an append-only record for each model call, a month's sums for a tenant, and the cap. A call to a model that is not local is stopped when the month's spend and the most the call could cost are together more than the cap.
- **The turn** joins them in `internal/turn/route.go`. Eight lines of the existing files changed and seven were added; the journal lists them.
- **Added to the plan**: a `spend_cap` error kind in the seam; and two more commands, `tools/tenant setting` and `tools/tenant usage`, because without them the cap and the fallback chain could only be changed from code.
- Seven settings under `routing.` and `usage.` in `settings/models.json`.
- The standing check `spend cap` passes. `verify` is 16 of 16.

Every part of the proof passes. Left for the items that need them, and listed in the docs: the turn marks nothing as sensitive until recall (P6-05) brings sensitive content into one; the summary, memory and embedding rules have no caller yet; and the warning near the cap is in the server's log and in `tools/tenant usage`, with no screen until P11-02.

Docs: `docs/architecture/routing.md` and `docs/architecture/usage.md`. Decisions 0160 to 0163. Journal: `docs/journal/2026-11-25-models-008.md`.

