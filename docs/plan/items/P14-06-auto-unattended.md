# P14-06. Auto for unattended work

Status: todo
Lane: agents
Depends on: P14-04, P8-01, P8-03
Owner gate: None.
Built without the gate: Everything.
Scope: Sub-agents, background tasks and scheduled runs start on Auto, each turn routed afresh, unless their caller names a model. Held parents and the cap behave as decisions 0483 and 0392 already say.
Proof: A scheduled run and a background task on Auto use the router and record verdicts. A held parent's sub-agent reaches no hosted model.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Unattended work", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
