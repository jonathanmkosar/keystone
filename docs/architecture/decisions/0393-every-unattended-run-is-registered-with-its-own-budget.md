# 0393. Every unattended run is registered by its request id, with its kind and its own budget

November 25, 2026 · decided in P8-05

## The question

The plan gives each background run, schedule and sub-agent its own budget. P8-01 gave a background task one, found by the gate through the `jobs` table. Scheduled runs are background tasks, and sub-agents (P8-02) do not exist yet and may not be tasks at all. How does every kind of unattended run get a budget the turn runner enforces, with a seam P8-02 can use without changing the gate?

## The decision

**A run registry**, the table `unattended_runs`, keyed by the request id every turn of the run is sent under, with the run's kind (`task`, `schedule`, `subagent`), its id, its title and its budget. `unattended.Register` writes a run, and a caller writes it in the same transaction as the run's own row, before any turn can be sent.

- **The gate is the registry's.** `jobs.Gate`, the turn runner's gate since P8-01, is now `unattended.Gate`: it finds the run from the turn's request id, sums the run's usage records, and refuses a paid call that could take it past its budget, with a message that names the kind ("The scheduled task “Harbour note” has spent …").
- **Budgets come from settings by kind**, with an override for one item: `agents.task_budget_usd` with `budget_usd` on the start route (a person's, never a model's), `agents.schedule_budget_usd` with `budget_usd` on a schedule (for each of its runs), and `agents.subagent_budget_usd`. The budget is kept on the run when it starts.
- **Background tasks register as `task`, scheduled runs as `schedule`.** The migration registers every task that was there.
- **A sub-agent registers as `subagent`** when P8-02 builds it, sends its turns under its request id, and gets the budget, the fallback and the notices with no change here. A stand-in sub-agent proves it today.

## Why

- Every kind's spend is already the sum of the usage records of its request id, which every call writes. Keying the registry by that id lets one gate serve every kind with one query, the way P8-01's did for tasks.
- A sub-agent's own table, whatever P8-02 makes it, need not be known to the gate. The kind and the id are enough to name it to a person.
- A model cannot give a task a budget: the tool takes no budget, so content the agent read can never raise what it may spend.
- A schedule's budget is for each run because each run is one task with one request id; a monthly budget across runs is a different rule, left for when it is asked for.

## What it costs

- A task's budget is written twice, on its `jobs` row (for the API) and in the registry (for the gate). Both are written in one transaction and never change.
- Two calls of one run at the same moment can each pass the check and together pass the budget by one call's cost, as at the cap.
- A sub-agent budget exists as a setting before anything uses it.
