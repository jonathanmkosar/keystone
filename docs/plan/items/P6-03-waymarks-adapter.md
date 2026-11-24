# P6-03. Waymarks adapter

Status: todo
Lane: knowledge
Depends on: P6-01
Owner gate: none
Scope: API client for one Waymarks instance per tenant; one token per role (reader for chat and sub-agents, intake for the memory pass); outage handling that never breaks a chat.
Proof: Passes the contract suite against a Waymarks test tenant; a chat finishes with Waymarks down.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

