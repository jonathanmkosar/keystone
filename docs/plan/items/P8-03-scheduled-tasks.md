# P8-03. Scheduled tasks

Status: doing (agents)
Lane: agents
Depends on: P8-01
Owner gate: none
Scope: Schedules (hourly, daily, weekly, weekdays, one-time) in the tenant's time zone; each run is a chat turn; a missed run happens once.
Proof: A missed run fires once after a simulated outage; runs appear as chats.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan, November 25, 2026 (agents, on `agents/p8-03-scheduled-tasks`):

- A new package, `internal/schedules`, with one migration stamped `20261125T180000`: a row for each schedule (name, prompt, recurrence, model or the chat's default, incognito, enabled, the zone its next due time was worked out in, the next due time) and a row for each run (the schedule, the due time it was for, whether it was late and how many due times it stands for, its chat and its task).
- Recurrences: hourly at a minute, daily, weekdays, weekly on chosen days at a time, and once at a date and time, in the person's zone: a new tenant setting `agents.time_zone`, an IANA name, empty for this machine's. The next due time is worked out from wall-clock time in that zone: a time the clock skips that day runs as long after the change as it was after the start of the gap, a time that happens twice runs once, at the first, and hourly runs every real hour. The zone database is compiled in (`time/tzdata`), so a machine without one still knows the names.
- A scheduler in the server, a goroutine under the turn runner, that looks for due schedules every `agents.schedule_poll` and when the server starts. A due schedule starts one run: a chat titled by the schedule and the date, and a background task working in that chat (`internal/jobs`, no second runner), so the run is the chat's turn and appears in the sidebar. Its next due time is moved past now at once, in the same transaction as the run, so a missed run happens once: when the server was down over one or more due times, one run starts, marked late, standing for every time it missed. A schedule whose last run is still going is not started again until it ends.
- In `internal/jobs`, a new file only, apart from one line in `deliver`: `StartIn`, a task that works in the chat it was started from, whose result is the chat's own reply, so no note is written when it is done; a note says why when it was stopped or failed. `Read`, to read one task.
- A per-person pause switch, the tenant setting `agents.schedules_paused`, that holds every schedule: no run starts, and the times that pass are not made up afterwards. It does not hold background tasks or a run under way (P8-05 owns the full switch).
- Ten routes under `/api/v1/schedules`: list, create, read with runs, edit, delete, pause, resume, run now, and pause all and resume all; `docs/api/openapi.json` regenerated; the route walks cover them as they cover every route.
- The Scheduled screen at `/scheduled`, from `design/screens/Scheduled.html` and the tokens, in new files under `web/app/src/`.
- Proof: tests, and a standing `verify` check, `scheduled tasks`: a daily schedule whose due time passes while the server is stopped fires once, and only once, when it starts again, late, and its run is a chat with the result; a paused schedule does not fire, and the switch holds them all; the next due time is right across a daylight-saving change.

Left for later: a budget of its own for each schedule and the full pause switch (P8-05), a list of runs on Operations (P11-03), and a schedule a model can make with a tool.

