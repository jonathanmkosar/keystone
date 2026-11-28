# P14-03. The router

Status: todo
Lane: models
Depends on: P14-01, P14-02
Owner gate: None.
Built without the gate: Everything.
Scope: A new package, `internal/router`: the ladder and its size limits, the five rules, the verdict with its confidence step and timeout, the reason line, and the router's settings. It reads nothing itself: the turn passes it the settings and the facts, the step the previous message used among them. It only narrows or orders what `routing.Plan` allows. `docs/architecture/routing.md` and `PLAN.md`'s routing line change with it.
Proof: Table tests for each rule, the confidence step, the timeout and the rules-only path. A held chat never gets a hosted step, whatever the verdict says.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "The ladder" to "The verdict", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
