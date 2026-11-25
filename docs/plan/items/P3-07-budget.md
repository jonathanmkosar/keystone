# P3-07. Token budget

Status: done
Lane: models
Depends on: P3-02
Owner gate: none
Scope: Provider token counts where offered, estimates with a safety margin elsewhere; a call that cannot fit is refused before it is sent.
Proof: The context-fit check passes for every registered model.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

A new package, `internal/budget`, with no network import and no store.

- `Counter`, an optional interface a `seam.Provider` may also implement: `CountTokens(ctx, req) (int, error)`, the provider's own count of a request's input. It is declared in `internal/budget`, and an adapter meets it by having the method, so the three real adapters add it later with no change here and no import of this package.
- `Estimate(req, opts)`: Keystone's own count from the characters and bytes of the request, for a provider that offers none.
- `Count(ctx, provider, req, opts)`: the provider's count where it has one, and otherwise the estimate plus the safety margin. A provider count that fails falls back to the estimate.
- `Check(ctx, model, provider, req, opts)`: refuses a call whose input plus the room kept for the reply is more than the entry's `ContextWindow`, with a `*TooLarge` error that says by how many tokens. `Guard(model, provider, opts)` wraps a provider so the refusal happens inside `Stream`, before anything is sent, as a `too_long` error event.
- The tunables are settings in `settings/models.json`, read by `budget.FromSettings` and passed in as `Options`: characters per token, the margin, and the flat counts for a message, a tool, an image and a file named by reference.

Proof: unit tests in the package, and a new standing check `context fit` in `tools/verify/contextfit.go`. For every entry in `fixtures/models/examples.json`, and for the fake provider itself, a request over the window is refused with the number of tokens over, and one under it is accepted. A provider that counts is believed over the estimate.


### Result

Built as planned, in `internal/budget`, with two names changed from the plan: the count is `Tokens`, and `Measure` checks a count the caller already has. `InputRoom` was added for the context builder.

- `Check` refuses a call that cannot fit with a `*TooLarge` that says by how many tokens, and `Guard` does the same inside `Stream` as a `too_long` error, before anything is sent.
- A provider that implements `budget.Counter` is asked for its own count. Everything else is estimated with the margin. No real adapter counts yet; each adds `CountTokens` in its own item with no change here.
- Six settings under `budget.` in `settings/models.json`.
- The standing check `context fit` passes for every model in `fixtures/models/examples.json` and through the fake provider. `verify` is 14 of 14.
- Packing the largest test chat under each window, the plan's wording of the check, joins it with P5-01.

Doc: `docs/architecture/budget.md`. Decisions 0130 and 0131. Journal: `docs/journal/2026-11-24-models-007.md`.
