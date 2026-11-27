# P14-01. Effort on the seam

Status: todo
Lane: models
Depends on: P3-01, P3-08
Owner gate: None.
Built without the gate: Everything.
Scope: `seam.Request` gains `Effort`. Each of the three adapters turns it into what its provider takes, by the table in the spec. `Effort` wins over `ThinkingTokens` when both are set. New settings for the Anthropic and Chat Completions adapters.
Proof: Recorded exchanges show each effort in each adapter's request in the spec's shape. A request with no effort is sent exactly as before.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Effort on the seam", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
