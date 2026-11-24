# P8-06. Wiki upkeep jobs

Status: todo
Lane: agents + knowledge
Depends on: P8-03, P6-03, P6-06
Owner gate: none
Scope: The Waymarks agent-kit jobs (compile, lint, upkeep, fix issues) run as Keystone scheduled agents on a local or trusted model, each with its own Waymarks token and grant, so sources added by the memory pass become cited pages. The built-in store gets a simple compile job.
Proof: On a Waymarks test tenant, a fact told in chat appears on a cited page after the next compile run, and the Waymarks audit shows each job under its own token.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

