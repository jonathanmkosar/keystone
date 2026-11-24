# P3-08. Routing, fallbacks, usage and the spend cap

Status: todo
Lane: models
Depends on: P3-03, P3-04, P3-05, P3-06, P3-07
Owner gate: none
Scope: Rule routing by task kind; privacy routing (sensitive content to local or trusted only); fallback chains; a usage record per call; the monthly cap with warning and stop.
Proof: Every call has a usage record; a call over the cap is stopped; a failing endpoint falls back; the spend-cap check is added.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

