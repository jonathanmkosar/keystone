# 0516. A routine becomes a paused schedule that keeps its times on the clock

November 26, 2026 · decided in P12-03

## The question

A New Keystone routine is a saved prompt with a cadence, in one fixed time zone, America/Chicago. A Keystone schedule is a prompt with a recurrence, in the person's own zone (`agents.time_zone`, the computer's when empty). The cadences nearly match: New Keystone has `manual`, which Keystone has not, and its weekly routine names one day where Keystone's names several. How does one become the other, and what of the morning brief, which New Keystone's code wrote and which here a model writes?

## The decision

- **Each routine that is `active` or `paused`, with a cadence Keystone has, becomes a schedule**: hourly, daily, weekdays, weekly on its one day, or once at a time still to come. Its name, prompt and times are kept as they are. It is made by `schedules.Scheduler.Create`, the function the routes call, so every check a schedule passes, it passes.
- **Every one arrives paused**, whatever it was in New Keystone. The person switches it on.
- **Times are kept as they read on the clock.** A person with no `agents.time_zone` of their own is given New Keystone's (`chats.import_time_zone`), so 07:00 there is 07:00 here. A person with a zone of their own keeps it, and the report says the two differ.
- **Left out, and counted by reason**: `manual` routines, routines only proposed (`pending`), `deleted` ones, one-time ones whose time has passed, and any past the most schedules a person may have.
- **The morning brief** is brought like any routine, with the prompt New Keystone gave it, and is known by its id (a hash of a fixed phrase in New Keystone's source), so the report can say it is among the schedules without reading a name.
- A run of a routine in New Keystone is a chat here, titled as a scheduled run's is, and is not tied to the schedule as one of its runs.

## Why

- Paused is the only safe arrival. A routine that runs here and still runs in New Keystone would send two briefs, and a schedule's run is a model call with the person's tools, which New Keystone's run was not. He should read the prompt and try it once first.
- A model writes the brief here, from Gmail and Calendar through the connectors, where New Keystone's own code assembled it. The prompt New Keystone kept, one line asking for the day's mail and calendar brief, is still what the person asked for, so it is kept, and the runbook says to read it and add what the brief should hold before switching it on.
- Converting 07:00 in Chicago to another zone's clock would be right only if the person had moved and wanted the brief at Chicago's morning. A person who set a zone here meant their schedules to run in it.
- Setting an empty zone, and not leaving the computer's, keeps the schedule right if Keystone later runs on a machine set to another zone. It is the owner's recommended answer to the open question on `agents.time_zone`, applied when it first matters.
- `manual` has no schedule to become: a schedule is due at some time. Making it a paused schedule at an invented time would work only until someone switched it on. It is one prompt the person can paste into a new schedule and use Run now.
- A routine New Keystone only proposed was never the person's choice, and a deleted one was their choice to remove.
- A schedule's runs are rows that point at a background task. An imported run has none, and a made-up one would read as a task that ran here.

## What it costs

- A routine that was active must be switched on by hand. That is one click, and is the point.
- A person whose own zone differs gets schedules at the same clock time in a different zone. The report says so; they change the time if they meant otherwise.
- The import changes one of the person's settings, `agents.time_zone`, when it was empty and a schedule is brought. The report and the audit entry say it did.
- A schedule and the note that it came from a routine are two writes, since `Create` opens its own transaction. A run cut short between them is mended by the next: it finds the paused schedule with exactly that name, prompt and times that no routine is noted for, and notes it instead of making a second.
