# P14-03. The router

Status: doing (models)
Lane: models
Depends on: P14-01, P14-02
Owner gate: None.
Built without the gate: Everything.
Scope: A new package, `internal/router`: the ladder and its size limits, the five rules, the verdict with its confidence step and timeout, the reason line, and the router's settings. It reads nothing itself: the turn passes it the settings and the facts, the step the previous message used among them. It only narrows or orders what `routing.Plan` allows. `docs/architecture/routing.md` and `PLAN.md`'s routing line change with it.
Proof: Table tests for each rule, the confidence step, the timeout and the rules-only path. A held chat never gets a hosted step, whatever the verdict says.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "The ladder" to "The verdict", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `models/p14-03-router`, cut from `reboot` at `ad7cff9`.

The spec, revised on November 27, 2026, lists four rules (held chat, needs, size, local machine down); the fifth, the chat's floor, is gone. The ladder's own skip of a name that is not a usable model is done by `routing.Plan` with the first two rules.

What will be built:

1. **`internal/router`**, a new package and a guard of fixed rule 3 (listed in `fixedRuleGuards`; it imports neither `settings` nor `internal/tenantsettings`, itself or through another package):
   - `ParseLadder` (names, `name<=tokens` limits, refusals for a bad limit or a name twice) and `ParseStepNotes` (`name=words;...`).
   - `Settings`, the parsed values, with `Load(value func(key) (string, bool))` over the setting keys, which the turn calls with its own reader, so the package reads nothing itself.
   - `Facts`: the newest message's text, estimated tokens, room kept for the reply, images, files, tools offered, the previous message's step, turns so far, held, and when a local endpoint last failed with `unavailable`, with the time now.
   - `Candidates`: each ladder step is asked of `routing.Plan` on its own (kind `chat`, held as sensitive, the needs), so a step is kept only if the plan would route to it: the router narrows and orders, never widens. Then the size limit and the window, then the local machine. Each step left out is listed with why.
   - `Route(ctx, Input) (Result, error)`: the rules, then with two or more candidates the verdict through `decision.Choose` (sensitive for a held chat) and a `Verdict` function the turn passes (open and `decision.Run`), under `router.verdict_timeout`; the confidence step; the rules-only path for no decision model, a timeout, any error, and a local decision model that is down (local steps then skipped, notice `local_unavailable`); effort no higher than `router.max_effort`; the reason line written by Keystone, with "switched from" when the step differs from the previous message's. A held chat's result is checked a last time: never a model that may not read sensitive content.
   - `Stronger(Input, answered)`: the candidate one above the step that answered, for P14-04's "Try a stronger model".
   - Refusals with codes: `sensitive`, `local_unavailable`, `no_model`, `no_ladder`.
   - `Record`: the verdict's shape for P14-04's `router_verdicts` row (facts shown without the text, step answered, probability, effort, moved up, timed out, rules only and why, latency).
2. **Settings** in `settings/models.json`: the router's own six (`router.min_confidence`, `router.verdict_timeout`, `router.verdict_max_chars`, `router.default_effort`, `router.max_effort`, `router.local_down_for`, install) and the three of the admin's policy the router reads (`router.ladder`, `router.decision_model`, `router.step_notes`, tenant-admin), names, defaults and scopes as the spec gives them. `router.auto_default`, `router.may_switch_off` and `router.allowed_models` stay with P14-04 and P14-05, which read them.
3. **Proof**: table tests for each rule, the confidence step, the timeout, the rules-only paths, the reason lines, the effort cap and `Stronger`; a held chat never gets a hosted step whatever the verdict says. A case in the `sensitive content` check (`tools/verify/router.go`): a held chat with a verdict naming a hosted step at 0.99 gets a local step, and a held chat with only hosted steps is refused.
4. **Docs**: `docs/architecture/router.md` with its `componentDocs` line and overview row, `routing.md`, `PLAN.md`'s routing line, decisions from 0670, journal `models-011`.

Left to others: wiring it into the turn, `router_verdicts`, the `route` event and the `stronger` route (P14-04); the admin's other three settings and refusals (P14-05); the live check (P14-08).
