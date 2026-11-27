# Unattended spend and the pause switch

Folders: `internal/unattended`, the tasks' side in `internal/jobs/pause.go`, the schedules' side in `internal/schedules/unattended.go`, and one hook in `internal/turn/unattended.go`

Work that runs without the person watching: background tasks ([jobs.md](jobs.md)), the runs of scheduled tasks ([schedules.md](schedules.md)), and, once P8-02 lands, sub-agents. The plan's words: each background run, schedule and sub-agent has its own budget; at the monthly cap, unattended work falls back to local models or stops, and says so; a per-tenant switch pauses all agents and schedules at once.

Built in P8-05. Decisions [0390](decisions/0390-one-switch-pauses-every-agent-over-the-schedules-switch.md) to [0393](decisions/0393-every-unattended-run-is-registered-with-its-own-budget.md) say why it is built this way.

## The shape

Three things, and nothing that runs a turn:

- **The run registry.** Every unattended run is a row in `unattended_runs`, keyed by its own request id, with its kind (`task`, `schedule`, `subagent`), its id, its title and its budget. Every turn of the run is sent under that request id, so its spend is the sum of the usage records that carry it, and the budget gate finds the run from a turn's request id. A kind of run gets a budget by registering (decision [0393](decisions/0393-every-unattended-run-is-registered-with-its-own-budget.md)).
- **At the cap or a run's budget**, a hook the turn runner asks when a route moves past a model lets the call go on to a local model and keeps a notice of it, or ends the call when the person has switched fallback off for unattended work (decision [0392](decisions/0392-unattended-work-at-the-cap-falls-back-with-a-notice-or-stops.md)).
- **The switch**, the person's setting `agents.paused`, and its routes. What runs unattended work reads the switch at every look, so it holds whichever way it was turned on (decisions [0390](decisions/0390-one-switch-pauses-every-agent-over-the-schedules-switch.md) and [0391](decisions/0391-a-paused-task-is-stopped-through-its-context-and-goes-on-with-a-new-round.md)).

Interactive chats are untouched: the hook answers nothing for a turn that is no run's, so a chat at the cap falls back as decision [0160](decisions/0160-a-capped-call-falls-back-to-a-local-model.md) has it, and the switch pauses no chat.

## Budgets for each run

| Kind | Its budget | An override for one |
| --- | --- | --- |
| A background task | `agents.task_budget_usd`, $1 as shipped | `budget_usd` on `POST /chats/{id}/tasks`. A model's `start_background_task` cannot give one: a model never sets its own budget. |
| A run of a scheduled task | `agents.schedule_budget_usd`, $1 as shipped, for each run | `budget_usd` on the schedule, for each of its runs |
| A sub-agent (P8-02) | `agents.subagent_budget_usd`, $0.25 as shipped | whatever P8-02 gives `unattended.Register` |

A budget is kept on the run when it starts, so changing the setting changes the runs that start afterwards. It is enforced as P8-01 enforced a task's: before each call to a model that is not local, after the monthly cap, the turn runner asks `turn.Options.Gate` (still `jobs.Gate`, which is now `unattended.Gate`). When the run's spend and the most the call could cost are together more than its budget, the call is not sent: the cap's own kind of refusal, `spend_cap`, whose message names the kind and the run: "The call to … was not sent. The scheduled task “Harbour note” has spent $0.98 of its budget of $1.00, and this call could cost up to $0.05." The monthly cap is asked first and still applies to every call.

A schedule's budget is for each of its runs, not for a month of them.

## At the cap or a run's budget

When the monthly cap or the run's budget refuses a call of an unattended run, the route moves on to its next model, and before it does, the turn runner asks `turn.Options.Moved`, which `internal/app` sets to `unattended.Moved`:

- **Fallback on** (`agents.unattended_fallback`, as shipped): the call goes on, and a notice is kept in `unattended_notices`. A local model behind it answers at no cost; a paid one further on is asked of the cap and the budget again. Once the round has ended, the run's chat is told, in a note Keystone writes: the refusal, then "So house-small, a local model, wrote the reply in place of paid-large." The note of a task's end, in the chat it was started from, says which model wrote its work. A notice is told only when the model it went on to wrote a reply in that turn; one that was refused in turn is followed by another notice.
- **Fallback off**: the call ends with a `spend_cap` error that says why ("Unattended work does not go on with another model when this happens: that is switched off in your settings, so the background task stopped here."). The round fails, the task ends with that message in its note, and a task's own chat is told too. A run that works in its own chat, as a scheduled run does, is told by the note of its end alone.
- **No other model in the chain**: as before P8-05, the round fails with `spend_cap` and the task's note says why.

**The audit log records it either way**: the refused call is a model call with the outcome `refused`, and what came back is the refusal with its reason; the local model's call follows it under the same request id. The usage records hold the refused call at no cost. When the hook ends the call, the next model is never started, so the log has no entry for it.

## The switch that pauses every agent and schedule

**The switch is the person's setting `agents.paused`**, over the schedules' own switch, `agents.schedules_paused` (decision [0390](decisions/0390-one-switch-pauses-every-agent-over-the-schedules-switch.md)). One person's switch touches only their own runs: it is a row in their own database, and every part reads it from the store of the person it works for.

While it is on:

- **Every running background task and scheduled run is stopped within seconds.** A task's goroutine reads the switch at every look; the pause route wakes every task at once, and a switch turned on from Settings → General is seen within `chats.stream_poll`. The task's round is stopped as the stop route stops a turn: its model call is cancelled through its context, recorded as a stopped call in the audit log and the usage records, and what it had written is kept. The task is marked **paused**, not failed: its status is `paused`, its progress says so, and a note in its chat says its last step was cut short.
- **A task started meanwhile is held**: it is written, its status reads `paused` with the progress "Held: …", and it calls nothing. This holds a model's `start_background_task` and a schedule's Run now too.
- **No schedule starts a run**, as with the schedules' own switch: each due time that passes is moved on, and each schedule shows as `held` while `SchedulesReply.paused`, the schedules' own switch, stays as it was.
- A paused or held task still counts against `agents.task_open_max`, and can still be stopped: a stop the person asks for wins over the switch, and the task ends as stopped.

When it goes off (decision [0391](decisions/0391-a-paused-task-is-stopped-through-its-context-and-goes-on-with-a-new-round.md)):

- **A paused task goes on with a new round**, sent with `agents.task_unpause_text`, as a round a restart cut short goes on with `agents.task_resume_text`. The new round counts against `agents.task_max_rounds`. A held task starts.
- **Nothing paused is made up**: the resume route moves on every schedule's due time that passed before the switch goes off, as decision [0343](decisions/0343-the-pause-switch-holds-schedules-and-nothing-is-made-up.md) does for the schedules' switch.

A switch that cannot be read counts as on: unattended work does not go on when the person's wish cannot be known.

## The routes

Every path starts with `/api/v1`. Each reaches only the signed-in person's switch and runs.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /agents` | | `SwitchReply` |
| `POST /agents/pause` | No body. | `SwitchReply`, once every run that was working has stopped, or after `agents.pause_wait`. |
| `POST /agents/resume` | No body. | `SwitchReply`, with the switch off. |

`SwitchReply` is `paused`, the switch; `working`, how many of the person's background tasks and scheduled runs have work under way (right after a pause, the ones that had not stopped within `agents.pause_wait`); and `held`, how many the switch holds.

Beside these, P8-05 adds to routes that were there:

- `Task.status` may be `paused` ([jobs.md](jobs.md)), and `StartBody` takes `budget_usd`.
- `Schedule` has `budget_usd`, its own budget for each run when it has one, and `run_budget_micros`, the one each run has; `ScheduleBody` takes `budget_usd`; `Schedule.held` is true under either switch; a run's `status` may be `paused` ([schedules.md](schedules.md)).

## On the screen

One bar with the switch, `web/app/src/AgentsSwitch.tsx`, drawn under the Scheduled screen's header and above the task cards in a chat: running, **Pause all agents**; paused, the words that say what waits and **Resume all agents**. Settings → General draws the setting from the registry like every other. A paused task's card reads "Background task, paused" with the server's words and keeps Stop; a paused run's pill is "Paused". The schedule form takes a budget for each run, and the schedule's BUDGET fact says whether it is its own. There is no mockup for any of it: the look is a first draft from the tokens until the agent mockups (P1-05). [web.md](web.md) has the calls and what was seen in a browser.

## Tables

In `internal/tenant/migrations/20261125T224500_unattended_spend.sql`.

| Table or column | Holds |
| --- | --- |
| `unattended_runs` | One row for each run: its request id (the key), kind, id, title, budget and when it was registered. The migration writes in every task that was there, a task that works in its own chat as a scheduled run. |
| `unattended_notices` | One row each time a call of a run was refused at the cap or its budget: fallback or stop, the model refused, the model it went on to and its class, the refusal, and the note that told the run's chat (its turn, chosen before it is written, and whether it is). |
| `jobs.paused`, `jobs.paused_turn`, `jobs.pause_note_turn` | The switch holds the task; the round it stopped, which a new round follows when it goes off; and the turn of the note that says so, until it is written. The task's stored status stays `queued` or `running`, so it still counts as open; the API shows it as `paused`. |
| `schedules.budget_micros` | A schedule's own budget for each run, or -1 for the person's `agents.schedule_budget_usd`. |

## In code

The seam other lanes use, P8-02's sub-agents first:

    unattended.Register(ctx, tx, unattended.Run{Kind: unattended.KindSubagent, ID: id, RequestID: rid, Title: title, BudgetMicros: n})
    unattended.Budget(ctx, store, unattended.KindSubagent)   // the person's budget for a kind, in millionths of a dollar
    unattended.Micros(usd)                                  // a budget in dollars, checked
    unattended.ForRequest(ctx, db, rid), unattended.ForTurn(ctx, store, turnID)
    unattended.Spent(ctx, store, rid)                        // spend on models that are not local
    unattended.Gate                                          // turn.Options.Gate, through jobs.Gate
    unattended.Moved                                         // turn.Options.Moved
    unattended.Notices(ctx, store, rid), unattended.Summary(ctx, store, rid)
    unattended.Tell(ctx, store, rid, turnID, stops, write)   // writes a round's notices in its chat, once
    unattended.Paused(ctx, store), unattended.SetPaused(ctx, store, on)
    unattended.Routes(unattended.Options{Config, Log, Runs, Pause, Rearm, Resume})

A sub-agent registers in the transaction that writes its own row, sends its turns under its request id, and reads `unattended.Paused` in its loop as a task does; the gate, the hook and the notices then hold for it with nothing more.

`turn.Options.Moved` is the one new turn option: it is told when a route moves past a model and may end the call instead. `route.go` changed one line, so that a route's `Moved` hook asks it first. `internal/jobs`: `pause.go` is new; `runner.go` gained the switch's line in `next`, the notices' line before `after`, the round sent after a pause, the budget for one task and the run's registration; `inchat.go` the same registration and a scheduled run's kind; `store.go` the three columns and the `paused` status; `gate.go` is now the registry's gate. `internal/schedules`: `unattended.go` is new; `scheduler.go` reads both switches when it looks and when it shows a schedule, counts a paused run as under way, checks and writes a schedule's budget, and starts a run as a scheduled run with its budget. `internal/app` adds the option, the config and the routes.

## Settings

In `settings/agents.json`.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.paused` | false | tenant | The switch that pauses every agent and schedule. |
| `agents.pause_wait` | 10s | install | How long the pause route waits for the work under way to stop before it answers. |
| `agents.task_unpause_text` | see the file | install | The message of a task's round after the switch stopped the round before and went off. |
| `agents.schedule_budget_usd` | 1 | tenant | Each run's budget for a schedule with none of its own. |
| `agents.subagent_budget_usd` | 0.25 | tenant | A sub-agent's budget. Nothing uses it until P8-02. |
| `agents.unattended_fallback` | true | tenant | Whether unattended work goes on with the next model at the cap or its budget, or stops. |

`agents.task_budget_usd` ([jobs.md](jobs.md)) is a task's, and `agents.schedules_paused` ([schedules.md](schedules.md)) the schedules' own switch. Settings → General draws a control for each of the person's, the switch among them.

## Checked by

- `go test ./internal/unattended`: a sub-agent, the stand-in for P8-02, registered with a budget of a cent, is let through within it and refused past it with a message that names it, and a turn that is no run's is let through; the budgets of every kind as shipped and as set; the hook lets a chat and an endpoint's failure through, keeps a fallback's notice and tells it once its model has written, tells the run's chat once under a note turn kept before it is written, and stops with a notice when fallback is off; the routes wait for the work to stop, move the schedules on before the switch goes off, and one person's pause leaves another's switch off.
- `go test ./internal/jobs`, with the turn runner and the fake provider:
  - **The switch stops every running task within seconds** (`TestThePauseSwitchStopsEveryRunningTask`): two tasks whose model calls wait until they are cancelled are stopped by the pause route in less than five seconds, both calls are cancelled, each task is paused with a note in its chat, each cancelled call has its started and end entries and a usage record that says it was stopped, another person's task goes on and their switch stays off, a task started meanwhile is held and calls nothing, and switched off, the two go on with a round sent with `agents.task_unpause_text` and the held one starts; all three finish. A paused task can be stopped.
  - **At the cap** (`TestAtTheCapATaskFallsBackOrStops`): a task goes on with the local model in the chain, its chat and its note name it, its audit log holds the refused call and the local one, and its notice is there; with fallback off it stops, says so in both chats, and the local model is not called; and a chat of the person's own still falls back.
  - **Past its own budget** (`TestARunPastItsOwnBudgetStops`): a task given a tenth of a cent through the route stops and says so, one given five dollars runs while the person's budget is a tenth of a cent, a negative budget is refused, and a scheduled run past `agents.schedule_budget_usd` says it is a scheduled task.
- `go test ./internal/schedules`: the switch holds every schedule, each shows as held while the schedules' own switch is off, and nothing is made up after it; a schedule's own budget, a change that takes it away, a negative one refused, and a run registered as a scheduled run with its budget.
- `verify`, the standing check `unattended spend` (`tools/verify/unattended.go`): on the real server, with the shipped settings, steps 1 to 4 of the item's proof end to end through the API, with the cancelled calls' usage records and every ended call recorded. The switch's three routes are walked with every other route: `tenant isolation`, `calls recorded` and `no keys in output`.

## Not here yet

- **Sub-agents** (P8-02). The kind, `agents.subagent_budget_usd` and the registry are the seam; nothing registers a sub-agent, and the switch pauses one when P8-02 reads it in its loop.
  - Built in P8-02 ([sub-agents.md](sub-agents.md)): each sub-agent registers as `subagent` with its budget in the transaction that writes its row, its chat is told of its notices and its result carries their summary, and its supervisor reads the switch at every look and ends it as `paused` (decision [0484](decisions/0484-the-pause-switch-ends-sub-agents.md)). Sub-agents are not counted in `SwitchReply`.
- **A budget for a month of a schedule's runs.** The budget is each run's.
- **A list of every task and run**, across chats, with the switch, on Operations (P11-03).
  - Built in P11-03 ([operations.md](operations.md)): the page draws the switch, lists every task and run by state, and shows the budget of each run under way as a limit, its spend summed from its usage records as the gate sums it, with a watch level, `operations.watch_run_budget_percent`.
- **The memory pass, recaps and compaction are not paused.** They are part of a chat, not agents. Their spend has the memory reserve and the cap.
- **A paused round's spend.** A call cut short is recorded with no tokens and no cost, as any stopped call is ([usage.md](usage.md)).
- **Two calls at once** can each pass a budget's check and together spend more than it by one call's cost, as at the cap.
- **Research tasks** (P8-04, [research-mode.md](research-mode.md)) are unattended runs too: a task's own turns are a run registered as a background task named "Research: …", and each of its parts is a sub-agent's run. Its budget, `agents.research_budget_usd`, is one sum split between them, and `unattended.SetBudget`, in the new `internal/unattended/budget.go`, changes a registered run's budget as the task moves from its plan to its parts to its report. The task reads the switch at every look; the pause routes' counts leave it out.
