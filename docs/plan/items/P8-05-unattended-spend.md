# P8-05. Unattended spend and the pause switch

Status: doing (agents)
Lane: agents
Depends on: P8-01, P3-08
Owner gate: none
Scope: Budgets per background run, schedule and sub-agent; fallback to local or stop at the cap, with a notice; a per-tenant switch that pauses all agents and schedules.
Proof: At the cap, unattended work falls back or stops and says so; the pause switch stops every running job within seconds.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (November 25, 2026, agents)

What is built, in this order, each step ending green:

1. **A seam for every unattended run**, a new package `internal/unattended` with a table `unattended_runs` keyed by request id: a run's kind (`task`, `schedule`, `subagent`), its id, its title and its budget. Background tasks register when they start, a scheduled run as `schedule`; existing tasks are written in by the migration. The budget gate (`turn.Options.Gate`, today `jobs.Gate`) finds the run by the turn's request id and sums the run's usage records, so any kind gets a budget by registering. Sub-agents do not exist yet (P8-02): the kind, `unattended.Register` and `agents.subagent_budget_usd` are the seam they will use, tested with a stand-in run.
2. **Budgets from settings, with an override per item.** `agents.task_budget_usd` stays the default for a task, and a person may give one task its own (`budget_usd` on the start route; a model's `start_background_task` cannot). `agents.schedule_budget_usd` is each run's budget for a schedule, and a schedule may have its own (`budget_usd` on the schedule). `agents.subagent_budget_usd` is a sub-agent's.
3. **At the cap or a run's budget.** A new turn option, `Options.Moved`, is told when a route moves past a model; for an unattended run whose call the cap or its budget refused, it either lets the call go on to a local model and records a notice (decision 0160: name the model that wrote the reply), or, when `agents.unattended_fallback` is off, ends the call with a `spend_cap` error that says why. The notice is written in the run's chat after its round, and goes into the task's result note and the task's `notices`. The audit log holds the refused call (outcome `refused`, with the reason) and the local model's call. With no local model the run stops, as today, and its note says so. Interactive chats are untouched: the hook answers nothing for a turn that is no run's.
4. **The switch that pauses every agent and schedule**: the tenant setting `agents.paused`, over `agents.schedules_paused` (decision 0390). While it is on, no schedule starts a run, every running background task has its round stopped through its context (the model call is cancelled and recorded as stopped) and is marked `paused` with a note in its chat, and a task started meanwhile is held. Off again, paused tasks go on with a new round (`agents.task_unpause_text`), held ones start, and the schedules' due times that passed are not made up. Routes `GET /agents`, `POST /agents/pause` (waits up to `agents.pause_wait` for running work to stop), `POST /agents/resume`. The task goroutines read the switch at every look, so turning it on from Settings → General works within `chats.stream_poll` too.
5. **Screens.** One control for the switch on the Scheduled screen and above the task cards in a chat; Settings → General draws it from the registry. A paused task's card says so. The schedule form takes a budget; the start route takes one.
6. **The proof**, with the fake provider: tests in `internal/unattended` and `internal/jobs` (at the cap a task falls back to a local model and says so, or stops when fallback is off; a run past its own budget stops and says so; the switch stops every running task within a bound, with a model call that blocks until cancelled, holds a new one, touches only the person's own, and resumes them), and a `verify` standing check, `unattended spend`, on the real server.

Left for later: the sub-agent side (wired when P8-02 lands); a budget per month for a schedule (the budget is each run's); a list of every task (P11-03).

