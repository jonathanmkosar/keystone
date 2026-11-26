# 0390. One switch pauses every agent and schedule, over the schedules' own switch

November 25, 2026 · decided in P8-05

## The question

The plan asks for a per-tenant switch that pauses all agents and schedules at once. P8-03 built a switch for schedules alone, the person's setting `agents.schedules_paused` (decision [0343](0343-the-pause-switch-holds-schedules-and-nothing-is-made-up.md)), which holds every schedule and leaves background tasks running. Should P8-05 widen that switch to cover everything, or put a new one over it? And where does it live?

## The decision

**A new person's setting, `agents.paused`, over the schedules' switch.** While it is on, no schedule starts a run (exactly as the schedules' switch holds them, and nothing held is made up), every running background task and scheduled run is stopped and marked paused, and a task started meanwhile is held. The schedules' switch stays what 0343 made it: Pause all and Resume all on the Scheduled screen, holding schedules only.

- A schedule is held under either switch, and `Schedule.held` says so. `SchedulesReply.paused` stays the schedules' own switch, so Resume all never claims to release what the agents' switch holds.
- The switch has three routes, `GET /agents`, `POST /agents/pause` and `POST /agents/resume`, and Settings → General draws it from the registry like every person's setting. The screens that show background tasks and schedules draw it as one bar.
- What runs unattended work reads the switch at every look, from the store of the person it works for. Turning it on through the route also wakes every task at once and waits, up to `agents.pause_wait`, for the work under way to stop.

## Why

- Widening `agents.schedules_paused` would change the meaning of a setting whose key and words say schedules, and a person who paused schedules (to stop new runs while a long task finishes) would find their tasks stopped too.
- The two answer different wishes: "start nothing new on a timer" and "stop everything that spends without me, now". The second contains the first, so it sits over it rather than beside it.
- A setting needs no new table, is per person in their own database (fixed rule 1: one person's switch reaches only their runs), holds across a restart because it is read at every look, and gets a control on General for free.
- Reading the switch at every look, rather than only acting when the route is called, means it holds however it was turned on, from General or from the command line, within `chats.stream_poll`.

## What it costs

- Two switches touch schedules. The Scheduled screen says which one holds them.
- A switch turned on from General, not the route, stops the running tasks within a second or so rather than before the call answers, and is not waited for.
- Reading a setting at each look of each open task is one more small query per task per second.
