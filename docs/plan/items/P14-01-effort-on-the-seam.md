# P14-01. Effort on the seam

Status: done
Lane: models
Depends on: P3-01, P3-08
Owner gate: None.
Built without the gate: Everything.
Scope: `seam.Request` gains `Effort`. Each of the three adapters turns it into what its provider takes, by the table in the spec. `Effort` wins over `ThinkingTokens` when both are set. New settings for the Anthropic and Chat Completions adapters.
Proof: Recorded exchanges show each effort in each adapter's request in the spec's shape. A request with no effort is sent exactly as before.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Effort on the seam", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `models/p14-01-effort`.

1. First commit, before any adapter changes: the exact body each adapter sends for every recorded request, kept as `fixtures/<adapter>/golden/bodies.json`, with a test in each adapter that compares the body byte for byte. This is the proof that a request with no effort is sent as before.
2. `seam.Request.Effort` (`low`, `medium`, `high`, `xhigh`, `max`, or empty), checked by `ValidateRequest`. Nothing else in the seam changes.
3. Anthropic: `output_config.effort` for models matching `anthropic.effort_models`, with adaptive thinking when the entry has `thinking`. Other models with `thinking` get the budget from `anthropic.effort_budget_low`, `_medium` and `_high` through the existing thinking path. Nothing otherwise.
4. Responses: `reasoning.effort`, with `xhigh` and `max` sent as `high`, only for entries with `thinking`.
5. Chat Completions: `reasoning_effort`, only for entries with `thinking` and when `chat_completions.send_reasoning_effort` is true.
6. Recorded exchanges with each effort in each adapter. Settings in `settings/models.json`, read in `internal/adapters`. Docs, decisions 0620 onwards, journal `2026-11-27-models-009`.
