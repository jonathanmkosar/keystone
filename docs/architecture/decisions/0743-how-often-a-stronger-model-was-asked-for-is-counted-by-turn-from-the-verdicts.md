# 0743. How often a stronger model was asked for is counted by turn, from the verdicts

November 28, 2026 · decided in P14-07

## The question

"How often a stronger model was asked for." Try a stronger model marks the verdict of the turn asked about (`stronger_asked`) and writes a new turn whose own verdict says `rules_only_why = stronger_asked`. A turn routed again after its local step did not answer has two verdicts. A rewrite can itself be asked to be written again. What is counted, and out of what?

## The decision

- `turns`: the distinct turns of `router_verdicts` whose first verdict was written in the period. A rewrite is a turn of its own; a turn routed twice is one.
- `stronger_asked`: those of them that any of their verdicts marks `stronger_asked`.
- `rewrites`: those of them that were a stronger model's rewrite.
- The card says "a stronger model was asked for on `stronger_asked` of `turns` replies", with the share, and how many replies were written again a step up.

## Why

- The verdicts are the record the router keeps for every message on Auto, and the mark is set by the route that writes again, so both sides of the share come from one table.
- Counting by turn keeps a second routing from looking like a second message, and a rewrite asked again counts once for each reply it was asked for.

## What it costs

- A rewrite that the router then refused, when it ran, wrote no verdict: the reply asked about is counted as asked, and no rewrite is counted.
- Asking twice for the same reply counts once in `stronger_asked`; each rewrite that ran counts in `rewrites`.
