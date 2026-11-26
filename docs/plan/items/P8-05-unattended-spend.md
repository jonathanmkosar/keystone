# P8-05. Unattended spend and the pause switch

Status: doing (agents)
Lane: agents
Depends on: P8-01, P3-08
Owner gate: none
Scope: Budgets per background run, schedule and sub-agent; fallback to local or stop at the cap, with a notice; a per-tenant switch that pauses all agents and schedules.
Proof: At the cap, unattended work falls back or stops and says so; the pause switch stops every running job within seconds.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

