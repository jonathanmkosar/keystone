# P7-02. Approval policy

Status: todo
Lane: agents + interface
Depends on: P7-01
Owner gate: Approval of the agent mockups (P1-05), for the approval cards only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: Per-tool policy (always, never, ask); approval cards; turns that read outside content mark every side-effect tool as ask.
Proof: The approval check passes: a side effect after outside content always asks, whatever the tool's setting.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

