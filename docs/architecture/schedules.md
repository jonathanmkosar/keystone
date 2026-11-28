# Scheduled tasks

Folders: `internal/schedules`, the task's side in `internal/jobs/inchat.go`, and the screen in `web/app/src/Scheduled.tsx`

A scheduled task is a prompt on a schedule, in the person's time zone, whose every run is a chat turn. The plan's words: a prompt on a schedule; each run is a chat turn; a missed run happens once when the machine is back. The morning brief is one of these.

Built in P8-03. Decisions [0340](decisions/0340-a-scheduled-run-is-a-task-in-a-chat-of-its-own.md) to [0343](decisions/0343-the-pause-switch-holds-schedules-and-nothing-is-made-up.md) say why it is built this way.

## The shape

**A run is not worked here.** When a schedule is due, the scheduler makes a chat titled by the schedule and the date ("Harbour brief, Fri 27 Nov 2026"; an hourly schedule's and a run now's carry the time too), and starts a **background task** ([jobs.md](jobs.md)) that works in that chat: the task's first round sends the schedule's prompt as the chat's first message. So the run *is* the chat. It is in the sidebar and on the chats screen, every step streams and is kept in the chat record, every model call and tool call is in the audit log (as the actor `schedule`, under the task's own request id), a tool that changes something waits for the person's approval in the run's chat and never runs unapproved, the task's budget and the monthly cap hold, and a run that a restart cut short resumes as any task does. There is no second runner (decision [0340](decisions/0340-a-scheduled-run-is-a-task-in-a-chat-of-its-own.md)).

**A task in its own chat.** Every other task works in a chat of its own and writes a note of its result in the chat it was started from. A run's task is started from its own chat (`jobs.Runner.StartIn`), so its result is already there, as the chat's last reply: when it is done no note is written, and the reply is noted as the task's result (`note_message_id`). When it is stopped or fails, a note in the chat says so ("“Harbour brief” did not finish. …").

What this package keeps is each schedule, its next due time, and a row for each run that points at its chat and its task.

## When a schedule is due

A schedule recurs in one of five ways:

| Kind | Fields | Due |
| --- | --- | --- |
| `hourly` | `minute` | Every hour, at that many minutes past. |
| `daily` | `time` | Every day at `time`, HH:MM. |
| `weekdays` | `time` | Monday to Friday at `time`. |
| `weekly` | `days`, `time` | On each of `days` (`mon` … `sun`) at `time`. |
| `once` | `date`, `time` | On `date` (YYYY-MM-DD) at `time`, and never again. |

Times are **wall-clock times in the person's time zone**: the tenant setting `agents.time_zone`, an IANA name such as `Europe/Lisbon`, empty for the zone of the computer Keystone runs on. A name that is no zone Keystone knows is said on the screen, and the computer's zone is used. The zone database is compiled into the binary (`time/tzdata`), so Windows, which has none, knows every name. Across a change of the clocks (decision [0342](decisions/0342-times-are-wall-clock-times-in-the-persons-zone.md)):

- A daily run at 07:00 stays at 07:00: the hours between two runs that day are 23 or 25.
- A time the clock skips runs as long after the change as it was after the start of the gap: 02:30 runs at 03:30 when 02:00 becomes 03:00.
- A time that happens twice runs once, at the first.
- An hourly schedule counts real hours: it runs twice in the hour the clocks repeat, and not in the hour they skip.

`schedules.Next(recurrence, after, zone)` is the rule as a function. A new or changed schedule is due at its first due time after now: a time that has passed today does not run late. A one-time schedule whose time has passed is refused.

When the person's zone changes, every next due time is worked out again in the new zone, from now, and nothing runs for it.

## The scheduler

A goroutine under the turn runner, so closing the turn runner stops it and waits for it. It starts when the server does, after the turn runner's and the task runner's recovery, and looks at once, then every `agents.schedule_poll` at the longest, sooner when a schedule is due sooner, and at once when a schedule is made, changed or resumed. Each look, for each tenant:

1. A run whose start was cut short, or that waits to start, is started.
2. For each enabled schedule that is due: when the person's switch is on, its due time passes and is moved past now; when its last run is still under way, it waits; otherwise one run starts.

## A missed run happens once

When a schedule is due, **one run starts for every due time that has passed** (decision [0341](decisions/0341-a-missed-run-happens-once.md)):

- Its due time is the latest of them, and `missed` counts them: three when the server was down over three mornings.
- The schedule's next due time is moved past now **in the same transaction that writes the run**, so whatever happens next, the times it stood for are not run again.
- The database refuses a second run for one due time (a unique index on the schedule and the due time).
- A run that starts more than `agents.schedule_late_after` after its due time, or that stands for more than one, is **late**, with the reason: `stopped`, Keystone was not running at the due time (it passed before this start of the server), or `busy`, the run before was still going or too many tasks were open.
- A due time that comes while the schedule's last run is under way waits for that run to end, and then runs once, late, standing for every time that came meanwhile.

A run is started in steps, each noted on the run before the next: the row (`starting`), its chat, its task (`started`). A run cut short between them is started again at the next look, where it stopped. A run whose task cannot start because the person has `agents.task_open_max` tasks open waits, with an error that says so, and starts when one ends; a run whose chat was deleted before it began, or that cannot start at all, is `failed` with the reason.

## Pausing

- **One schedule**: `POST …/pause` and `…/resume`. A paused schedule starts no run; resumed, it is due at its first due time after now. The times that passed while it was paused are not made up.
- **Every schedule**: the person's switch, the tenant setting `agents.schedules_paused`, which `POST /schedules/pause-all` and `…/resume-all` set and which Settings → General shows too. While it is on, no schedule starts a run, and their due times pass and are moved on; when it goes off, every schedule is due at its first due time after now. Nothing is made up (decision [0343](decisions/0343-the-pause-switch-holds-schedules-and-nothing-is-made-up.md)).
- The switch holds schedules only. A run already under way goes on (it is a background task: it can be stopped in its chat), and it does not hold background tasks. **Run now** still runs. The plan's switch that pauses all agents and schedules at once is P8-05's.
- **Every agent and schedule** (since P8-05): the person's switch `agents.paused` sits over this one ([unattended-spend.md](unattended-spend.md), decision [0390](decisions/0390-one-switch-pauses-every-agent-over-the-schedules-switch.md)). While it is on, schedules are held as above and each shows as `held`, while `paused` in `SchedulesReply` stays this switch; a run under way is stopped and marked `paused`, and goes on when it goes off; a run started meanwhile, by Run now too, is held. The route that turns it off moves every due time that passed on first, so nothing is made up.

## The routes

Every path starts with `/api/v1`. `{id}` is a schedule's id, sixteen hex characters. Every route reaches only the signed-in person's schedules.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /schedules` | | `SchedulesReply`: `schedules`, oldest first; `paused`, the switch; `zone`; `max`. |
| `POST /schedules` | `ScheduleBody` | `Schedule`, 201. |
| `POST /schedules/pause-all` | No body. | `SchedulesReply`, with the switch on. |
| `POST /schedules/resume-all` | No body. | `SchedulesReply`, with the switch off. |
| `GET /schedules/{id}` | | `ScheduleReply`: `schedule`, its newest `runs` (newest first, up to `agents.schedule_runs_listed`), and `zone`. |
| `PUT /schedules/{id}` | `ScheduleBody`: every field is replaced. | `Schedule`. Its next due time is worked out again from now. |
| `DELETE /schedules/{id}` | | `{"deleted": true}`. The record of its runs goes; each run's chat stays, and a run under way goes on. |
| `POST /schedules/{id}/pause` | No body. | `Schedule`. |
| `POST /schedules/{id}/resume` | No body. | `Schedule`, due at its first due time after now. |
| `POST /schedules/{id}/run` | No body. | `Run`, 201: a run now, in a chat of its own, paused or held. It changes no due time. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | No such schedule in this person's record. |
| `bad_request` | 400 | A body that does not fit: no name, a name of two lines or longer than `chats.title_max_length`, no prompt or one longer than `agents.schedule_prompt_max_chars`, a recurrence that does not read, a one-time schedule whose time has passed, a model not in the person's registry. |
| `too_many_schedules` | 429 | The person has `agents.schedule_max` schedules. |
| `run_open` | 409 | Run now while a run of the schedule is under way. |

### `ScheduleBody`

| Field | Meaning |
| --- | --- |
| `name` | One line. |
| `prompt` | The message each run sends, in a new chat. |
| `recurrence` | `kind`, and `minute`, `time`, `days` or `date` as the table above says. |
| `model` | A registry name, or absent for a new chat's own model. |
| `incognito` | Each run's chat is incognito: no memory pass reads it ([memory-pass.md](memory-pass.md)). |
| `enabled` | Absent means on for a new schedule, and as it was for a change. |

### `Schedule`

| Field | Meaning |
| --- | --- |
| `id`, `name`, `prompt`, `recurrence`, `model`, `incognito`, `enabled` | As given, the recurrence in its one form (`time` as HH:MM, `days` in order from Monday). |
| `says` | The recurrence in words: "Weekdays at 07:00", "Mondays and Thursdays at 18:30", "Every hour at 15 past", "Once, on Tue 1 Dec 2026 at 09:00". |
| `held` | The person's switch is on. |
| `next` | The next due time, **in the person's zone**: the JSON time carries the zone's offset, so the screen shows the wall-clock time from the text itself, whatever the browser's zone. Absent while paused or held, and for a one-time schedule that has run. |
| `last_run` | Its newest `Run`. |
| `created`, `updated` | |

### `Run`

| Field | Meaning |
| --- | --- |
| `id`, `schedule_id` | |
| `trigger` | `schedule` (a due time came) or `now` (the person asked). |
| `due`, `started` | The due time it was for, and when it started, in the person's zone. For a run now both are when it was asked. |
| `late`, `late_reason` | Late, `stopped` or `busy`, as above. |
| `missed` | How many due times it stands for: 1, or more after a stop. 0 for a run now. |
| `status` | `starting`, the task's `queued`, `running`, `done`, `stopped` or `failed`, or `gone`: its chat was deleted, and its task with it. A run that could not start is `failed` with no task. |
| `waiting` | A call of the run waits for the person's approval, in its chat. |
| `progress`, `steps`, `spent_micros`, `began`, `ended` | From its task: where it is, the model calls finished, its spend on models that are not local, and when its work began and ended. |
| `chat_id`, `task_id` | Its chat, where its result is, and its task. |
| `error` | Why it failed, or why it waits to start, for a person. |

`Zone` is `name` (the setting, or absent for the computer's), `label` (the name, or "This computer's time zone (GMT)"), and `problem` when the setting names no zone Keystone knows.

## On the screen

`/scheduled`, which the sidebar already linked, follows `design/screens/Scheduled.html` (a draft, in the first round's colours) with the tokens: a header with **Pause all** (or **Resume all**) and **New scheduled task**; the list of schedules, each with its name, its recurrence in words, its last run's state as a pill, and its next due time; and the chosen schedule: a switch that pauses and resumes it, **Run now**, **Edit** and **Delete**, four facts (schedule and zone, model, budget, if a run is missed), the prompt, and the table of runs (when, result with late and failure reasons, how long it took, steps and cost, and a link to its chat). The form makes and changes a schedule: name, prompt, how often (hourly, daily, weekdays, weekly with day toggles, once with a date), the time, the model, incognito. Times are shown as the server sent them, in the person's zone. The code is `web/app/src/Scheduled.tsx`, `ScheduleForm.tsx`, `schedules.ts` and `scheduled.css`, with calls at the end of `api.ts`, the route in `App.tsx`, and, in `Tasks.tsx`, the words of a task card and note in a run's own chat. There is no step-by-step run panel: the run's chat is where its steps are.

## Tables

In `internal/tenant/migrations/20261125T180000_schedules.sql`.

| Table | Holds |
| --- | --- |
| `schedules` | One row for each schedule: its name and prompt, its recurrence (`kind`, `hour`, `minute`, `days`, `on_date`), its model, incognito and enabled, the zone its next due time was worked out in, and the next due time in UTC (empty when there is none). |
| `schedule_runs` | One row for each run: its schedule (it goes with the schedule), why it started, the due time it was for, how many it stands for, whether it was late and why, its state (`starting`, `started`, `failed`), the request id and actor its task runs under, its chat and its task, and why it failed or waits. A unique index refuses a second run for one due time. |

## In code

    sched := schedules.New(schedules.Options{Install: in, Log: log, Config: cfg, Jobs: jobsRunner, Limits: limits, Now: clock})
    routes = append(routes, schedules.Routes(sched)...)
    sched.Start(turns)                // after turns.Recover and jobsRunner.Recover; closing turns stops it
    sched.Tick(ctx)                   // one look at every tenant, as the goroutine does
    schedules.Next(rec, after, loc)   // when a recurrence is due next, in a zone
    schedules.Says(rec)               // the recurrence in words

    sched.List / Get / Create / Update / Delete / SetEnabled / RunNow / Hold   // what the routes call, on one tenant's store

    jobsRunner.StartIn(ctx, store, chatID, startedRequest, actor, body, model) // a task that works in the chat it is started from
    jobs.Read(ctx, store, taskID)                                              // one task

`internal/app` does all of this: `newSchedules` in `internal/app/schedules.go`, and lines in `app.go` (the config, the routes, and the start in `Handler`). `app.Config.Clock` is the scheduler's clock: nil is the machine's, and a check sets it to stand for time that passed while the server was stopped.

## Settings

In `settings/agents.json`. `schedules.ReadConfig` reads the install's, and `app.ReadConfig` calls it. The two person's settings are read when they are needed.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.time_zone` | empty | tenant | The person's time zone, an IANA name. Empty is the computer's. |
| `agents.schedules_paused` | false | tenant | The switch that holds every schedule. |
| `agents.schedule_poll` | 30s | install | How often the scheduler looks, at the longest. |
| `agents.schedule_late_after` | 2m | install | A run that starts later than this after its due time is late. |
| `agents.schedule_max` | 50 | install | The most schedules one person may have. |
| `agents.schedule_prompt_max_chars` | 8000 | install | The longest prompt. |
| `agents.schedule_runs_listed` | 30 | install | How many runs a schedule's page lists. |

A run is a background task, so `agents.task_budget_usd` is each run's budget, and `agents.task_open_max`, `agents.task_max_rounds` and the rest of [jobs.md](jobs.md)'s settings apply to it.

## Checked by

- `go test ./internal/schedules`, with a real HTTP server, the turn runner, the task runner, the fake provider and a clock the test sets:
  - **A missed run happens once after an outage**: a daily schedule at 07:00 in Lisbon made on the 24th, the server stopped, three mornings passed: the next start runs it once, late because Keystone was not running, standing for three due times; its chat is titled "Harbour note, Fri 27 Nov 2026" and holds the prompt and the reply, which is its task's result with no note; its model call is in the audit log as the schedule's; more looks and another start run nothing more; a paused schedule did not run; and the next morning's run is on time. This is the item's proof.
  - The next due time is right across a change of the clocks, in New York and Lisbon, both ways, for a skipped time, a doubled one, weekdays, weekly days and once; hourly runs every real hour; the latest due time and the count after a long stop; a recurrence in words, and every refusal.
  - The switch holds every schedule over a restart, and nothing is made up when it goes off, nor when one schedule is resumed. A due time while a run is under way waits and runs once, late, as busy, and run now is refused meanwhile. A run with no model fails and its chat says why. Another person reaches none of a schedule. Every refusal of a body, one schedule too many, a change and a delete. A change of zone moves the next due time and runs nothing, and a name that is no zone is said. An incognito schedule's run has an incognito chat, and deleting a schedule keeps its runs' chats. The database refuses a second run for one due time.
- `go test ./internal/jobs`: a task in its own chat has its reply as its result and writes no note, and a second start gives the first task; stopped, it writes a note in its chat.
- `verify`, the standing check `scheduled tasks` (`tools/verify/schedules.go`): on the real server as the test tenant, with the server's clock set by the check, the proof above end to end through the API, with the run's step (`current_time`) and its result in its chat and its two model calls and its tool call recorded as the schedule's, under the run's own request id, with two usage records; then a third start runs nothing more; the switch holds over another stop and nothing is made up; the next due time across a change of the clocks, alone and through the route; and another tenant gets 404 from every route of a schedule. It was seen to fail when a due time that had passed was moved on instead of run.
- The route walks in `verify` call the ten routes like every other: `tenant isolation`, `calls recorded` and `no keys in output`.

## Not here yet

- **A budget of its own for each schedule** and the switch that pauses all agents and schedules at once (P8-05). Each run has the background task budget, `agents.task_budget_usd`.
  - Built in P8-05 ([unattended-spend.md](unattended-spend.md)): each run's budget is the schedule's own (`budget_usd` on `ScheduleBody` and `Schedule`, and `run_budget_micros`, the one in force), or the person's `agents.schedule_budget_usd`, no longer `agents.task_budget_usd`; a run is registered as a scheduled run, so its refusals say so. The budget is each run's, not a month's.
- **A schedule a model makes** with a tool. A person makes them on the screen or through the routes.
- **Runs on Operations** (P11-03), across schedules.
  - Built in P11-03 ([operations.md](operations.md)): Operations lists each schedule with its next due time and its newest run, through `Scheduler.List`, and each scheduled run among the background tasks, with its budget as a limit while it runs.
- **A step-by-step run panel** as in the mockup: the run's chat has its steps, and the inspector its tokens.
- **Retrying a run that failed** for a provider's failure. A person can run it now.
- A chat made for a run, and the server stopped before the run noted it, is made again at the next start; the first stays, empty.
- **Runs on Auto** (P14-06).
  - Built in P14-06 ([unattended-auto.md](unattended-auto.md)): a schedule with no `model` has each run's chat on Auto when `router.auto_default` is on and the person has a ladder, so the run's turns are routed afresh, with a verdict under the run's task's request id; a schedule that names a model keeps it. Nothing in this package changed: `jobs.Runner.StartIn` decides.
