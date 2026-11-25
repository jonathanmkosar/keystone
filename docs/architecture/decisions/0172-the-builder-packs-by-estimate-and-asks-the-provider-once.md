# 0172. The builder packs by the estimate and asks the provider to count the result

November 25, 2026 · decided in P5-01

## The question

`budget.Guard` checks every call with the provider's own count where it offers one (decision [0130](0130-a-provider-counts-through-an-optional-interface.md)). The builder has to choose what to send before there is a request to count. Counting every candidate with the provider is a network call for each turn it weighs. Packing by the estimate alone can produce a request the provider counts as too large, which the guard then refuses. Which count does the builder pack with?

## The decision

**It packs with `budget.Estimate`, piece by piece, under a target whose count with the margin is within the limit.** Each system text, the tools, each item and each turn is estimated once. The pieces' estimates each round up, so their sum is never less than the estimate of the whole request.

**Then it counts the packed request once with `budget.Tokens`**, which asks the provider when it is a `budget.Counter`. That count goes in the record as the request's tokens.

**When the provider counts more than the limit, the request is packed again** under a target made smaller by as much as the provider counted more, and counted again, up to `chats.context_recount_max` times. If it is still over, it is returned with `Over` set and the guard refuses it.

**A provider's count that is lower than the estimate is believed and nothing is added.** The builder does not go back to fit more in.

## Why

- With no counting provider, which is the fake and most local models, the packed request is certain to pass the guard: the guard makes the same estimate with the same margin.
- With a counting provider the usual case costs one count, and the guard's own check is a second. A request that fits by the estimate with a 15 percent margin will nearly always fit by the provider's count.
- Scaling the target by the two counts' ratio reaches a fit in one more pass for any provider whose tokens are evenly smaller than the estimate assumes.
- Filling the room a lower count frees would take a search with a count at each step, for a few more old turns.

## What it costs

- A model reached through a counting adapter is counted twice for each call: once here and once by the guard. An item that finds this slow can pass the builder's count to the guard.
- Against a provider that counts, the estimate's lean leaves room unused: roughly a third of a window at the shipped values on English prose (decision [0131](0131-the-estimate-counts-characters.md)).
- The tokens of each part in the record are estimates even when the total is the provider's count, so the parts do not add up to the total for such a model. The record says which kind the total is.
