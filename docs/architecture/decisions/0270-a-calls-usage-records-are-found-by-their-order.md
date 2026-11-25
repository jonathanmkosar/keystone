# 0270. A call's usage records are found by their order

November 25, 2026 · decided in P5-04

## The question

The turn inspector shows each model call of a turn with what it was built from and what it cost. What it was built from is kept by step, in `turn_contexts`. What it cost is a usage record, one for each model call, which carries the request id of the API call that sent the turn, its kind, its model and its outcome, and no step. How are the two joined, and how is it proven that the inspector's totals are the usage records?

## The decision

**The totals are every usage record under the turn's request id, summed.** Every model call a turn makes is made under that id: its chat calls, the models of a route that did not answer, its title, the summaries made beside it. Nothing else is: one request sends one turn, and a turn that runs after a restart keeps the id it was sent under.

**A call's records are found by their order, kind by kind.** In the order they were written, a call's run of records ends at the one whose outcome is `ok` or `stopped`, or at the last record of its kind. The records before it in the run are the models of the route that did not answer, each written when the chain moved on. The chat calls' runs are matched to the steps in order.

**Every record is in exactly one call,** so the calls' totals add up to the turn's, and a test says so on every turn it inspects.

## Why

- It holds by construction for chat calls. A model of a route that did not answer is recorded before the next is tried, and a chat call whose last model did not end `ok` or `stopped` ends the turn. So only the last chat call can end any other way, and a run can never take in the next step's records.
- It needs no change to the usage table, which belongs to the models lane, is append-only, and is read by the spend cap, and no change to `Runner.call` or `Runner.record`, which other lanes are working beside. A column for the step would need both, and the fallback hook that writes a failed model's record does not know the step.
- The totals do not rest on the grouping at all: they are the sum of the records, which is what the proof asks.

## What it costs

- For summary calls the rule can be wrong in one case: a summary job that failed on its last model, followed in the same turn by another job, is shown as one call with a fallback. The records and the totals are still right; only the grouping of that display is off.
- A step whose call never ended, which only the server stopping can leave, has its record and no usage. A record whose context could not be stored is given the next step with no context. Both are shown as what they are.
- If the runner ever retries a chat call within a step, or goes on after a failed one, this rule must change with it. The test of a turn with a fallback and of a turn of three tool steps would catch it.
