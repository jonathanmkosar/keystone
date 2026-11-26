# 0522. A research task's budget is one sum, split between its own run and its parts'

November 26, 2026 · decided in P8-04

## The question

A research task is several unattended runs: its own turns, and a sub-agent for each part. The registry gives every run a budget of its own, checked against the spend of that run's request id (decision 0393). The item asks for one budget for the task, shown before it starts. How is one sum held across several runs?

## The decision

**The person's `agents.research_budget_usd` is the whole task's, and it is split so the runs' budgets never add up to more than it.** The task's own run starts with all of it, for the plan. Before the parts start it is lowered to `agents.research_report_share_percent` of the budget, or to what the plan spent if that was more, and the rest is split evenly between the parts. When the parts have ended it is raised to the budget less what they spent. `unattended.SetBudget` changes a registered run's budget.

**A part's budget is its share, and is not held to `agents.subagent_budget_usd`.** The task's run is registered as a background task named "Research: …".

## Why

- **The gate stays as it is.** Each run is still checked against its own budget before each call, so the fallback, the notices and the refusal's words are the ones already built. Nothing sums across runs at call time.
- **The order makes the sum safe.** The task's run is lowered before the parts are written, and raised only after they have ended, so at no moment could the runs together spend more than the budget.
- **What the parts leave goes to the report.** The gate refuses a call on the most it could cost, which for a long reply is far more than it does cost. A fixed share for the report would often refuse it with most of the budget unspent.
- **The sub-agent limit is about a model's call.** Decision 0482 holds what a model may give a sub-agent to the person's setting. Here no model sets anything: the person set the research budget, and the shares follow from it.
- **A kind of its own was not added.** A research task runs as a background task does, and the refusal reads "The background task “Research: …” has spent …". A new kind would change the registry's checks for one word.

## What it costs

- Parts get equal shares. A part that needs more than its share stops while another leaves money unspent, which only the report step gets back.
- A task whose plan cost more than the report's share leaves the report nothing until the parts end.
- Two calls at once can each pass a budget's check and together spend more than it by one call's cost, as at the cap.
- Research tasks are not on Operations' list of runs, which reads the task and schedule tables.
