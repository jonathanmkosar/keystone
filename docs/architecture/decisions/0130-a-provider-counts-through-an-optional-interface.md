# 0130. A provider counts tokens through an optional interface, declared by the budget

November 24, 2026 · decided in P3-07

## The question

Some providers will count a request's tokens before it is sent, and some will not. The budget has to use the count where there is one. Three adapters are being built at the same time as the budget, by other hands. Where does the count go so that neither side waits for the other?

## The decision

**One optional interface, `budget.Counter`, with one method:** `CountTokens(ctx, req seam.Request) (int, error)`, the request's input tokens.

**It is declared in `internal/budget`, not in the seam.** A provider is a `Counter` when the value its builder returns has the method. Go checks that by shape, so an adapter does not import the budget, and the budget names no adapter.

**`seam.Provider` is not changed.** A provider with no count is still a whole provider, and the fake stays as it is.

**A count that fails is not a failed call.** The budget falls back to its estimate with the margin. Only a cancelled context is passed on.

**`Guard` keeps the count.** The wrapper it returns is a `Counter` when the provider it wraps is one, so wrapping twice or asking later still finds it.

## Why

- A method on `seam.Provider` would make every provider answer it, and the honest answer for most is "I cannot". An optional interface says that by not being there.
- Declaring it beside its one reader means the seam, the registry and `internal/adapters` are untouched, and three adapters in flight do not meet in one file.
- A count is one more call to the provider. If that call is down or slow, the turn should still go out when Keystone's own count says it fits.

## What it costs

- Nothing tells an adapter's author the method exists except the docs, and a method with the wrong shape is silently not a `Counter`. An adapter that means to count should say so in a test: `var _ budget.Counter = provider` in its test file, which may import the budget.
- The count costs a round trip before each call. Whether to count every call or only those the estimate puts near the window is the caller's choice, with `Estimated` and `Measure`.
