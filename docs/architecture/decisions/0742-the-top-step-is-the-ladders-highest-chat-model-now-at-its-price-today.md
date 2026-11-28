# 0742. The top step is the ladder's highest chat model now, priced call by call at its price today

November 28, 2026 · decided in P14-07

## The question

"The same tokens priced at the ladder's top step." The ladder is a setting that keeps no history, its last name may not be in the registry, a price may change, and a reply's call counts four kinds of token, the cache's among them. Which step is the top one, at which price, and how are the tokens priced?

## The decision

- The top step is the highest name on `router.ladder`, as it stands now, that is a model in the person's registry, read without a problem, and takes chat calls (not an embedding or a decision model). The router skips a name that is not a usable model, so no message reaches higher.
- Each reply call's own four counts (input, output, cache read, cache write) are priced at the top step's price, rounded to the millionth call by call, with `models.Price.CostMicros`, as the turn prices a call when it ends; and added up. A refused call was never sent and costs nothing at either price.
- A past period is priced at today's ladder and today's price, as the report shows a past month against today's cap.
- What was saved is the top step's cost less what Auto cost, the decisions included. It may be below zero, and the card then says how much more Auto cost.
- With no ladder, a ladder that does not read, or no step of it in the registry, the top step has a `problem` in words, and its cost and what was saved are zero. What Auto cost is still shown.

## Why

- The same counts at another price is what "the same tokens" says, and the turn's own pricing keeps the two figures comparable to the cent.
- A top step chosen from the registry is the step the router could have used; a name it would skip would price the replies at a model nobody can reach.

## What it costs

- The cache counts are priced as they happened on the step that answered. Had every message gone to the top step, its cache would have been read more, since Auto's switches lose a provider's cache (the spec's "Switching costs the cache"); the comparison does not model that.
- A top step with no cache price in its entry prices cache reads and writes at nothing, as the turn would.
- Changing the ladder changes the comparison for every past period.
