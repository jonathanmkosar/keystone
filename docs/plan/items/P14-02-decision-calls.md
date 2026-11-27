# P14-02. Decision calls and the decision adapter

Status: todo
Lane: models
Depends on: P3-02, P3-03, P3-08
Owner gate: Ollama 0.35.1 or later on the owner's local machine, with the decision model pulled (live check only).
Built without the gate: All code, proven on recorded exchanges and the fake provider.
Scope: The capability `decision`, the seam's `Decide` call and its types, the `systemone` adapter, decision answers in the fake provider, and the audit entries and usage records of a decision call. The kind `decision` in `routing.Plan`, so a sensitive decision call is routed by the existing guard.
Proof: A recorded decision call returns each option's probability. A request over the size or question limit is refused before it is sent. A decision call with sensitive state never reaches a hosted entry. Every decision call has its audit entries and usage record.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Decision calls on the seam", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
