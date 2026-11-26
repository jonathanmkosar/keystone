# 0482. A model may ask a sub-agent for less budget, never more

November 26, 2026 · decided in P8-02

## The question

The item lets the model start a sub-agent "optionally with a model and a budget within the person's limits". A model's `start_background_task` takes no budget at all (decision 0393). What may a model give a sub-agent, and what happens to a budget above the limit?

## The decision

**`budget_usd` on a sub-agent is held to the person's `agents.subagent_budget_usd`**: a smaller one is kept, a larger one is lowered to the limit, and none means the limit. A budget that is not a number of dollars, or is negative, refuses the call. The budget is written with the sub-agent's run in the registry before its turn can be sent.

## Why

- A model can split work into parts of different sizes, and a small part should be able to spend less.
- Nothing a model writes can raise what it may spend: the person's setting is the ceiling, as for tasks.
- Lowering, not refusing, keeps a well-meant call working; the lane and the result show the budget it got.

## What it costs

- A model that asks for more is not told it got less, except by the budget its sub-agent's section shows.
