# 0343. The pause switch is a person's setting that holds every schedule, and nothing paused is made up

November 25, 2026 · decided in P8-03

## The question

The item asks for a per-person switch that holds every schedule; the plan's switch that pauses all agents and schedules at once is P8-05's. Where does the switch live, what does it hold, and when it goes off, or when one paused schedule is resumed, do the due times that passed meanwhile run?

## The decision

**The switch is the tenant setting `agents.schedules_paused`.** The Scheduled screen's Pause all and Resume all set it through `POST /schedules/pause-all` and `…/resume-all`, and Settings → General shows it with the person's other settings.

**It holds schedules, and only schedules.** While it is on, no schedule starts a run. A run already under way goes on: it is a background task, and the person can stop it in its chat. Background tasks are not held. Run now still runs, because the person asked. A switch that cannot be read counts as on.

**Nothing paused is made up.** While the switch is on, each due time that comes is moved on to the next, with no run. When it goes off, and when one paused schedule is resumed, the schedule is due at its first due time after now. A due time that passes while a schedule is paused or held is not a missed run.

## Why

- A setting needs no new table, is kept per person in their own database, is read at every look so it holds across a restart, and is where P8-05 can widen it into the switch for everything.
- Holding background tasks means stopping or parking work a person started on purpose, and it changes the task runner; that is P8-05's to decide with unattended spend. Holding schedules is what this item can promise.
- A person who pauses has chosen to skip those runs; making them up when they resume would do what they paused to prevent. A missed run, made up once, is for times the person did not choose to skip: when Keystone was not running.
- Reading an unreadable switch as on keeps unattended work from starting when the person's wish cannot be known.

## What it costs

- If the switch is turned off on Settings → General instead of the Scheduled screen, a due time that came in the few seconds since the scheduler last looked may still run, on time, once.
- Two switches will exist until P8-05 joins them: this one, and the plan's for everything.
