# 0341. A missed run happens once: one run stands for every due time that passed, and the next is moved in the same transaction

November 25, 2026 · decided in P8-03

## The question

The plan says a missed run happens once when the machine is back. When the server was down over one due time, or over many, what runs when it starts, and how is it made sure that nothing runs twice, whatever stops when?

## The decision

**One run for all of them.** When a schedule is due, the scheduler finds every due time that has passed since its stored next due time. One run starts. Its due time is the latest of them, and it counts them (`missed`). Never one run per missed time.

**The next due time moves with the run.** The run's row is written and the schedule's next due time is moved to its first due time after now in one transaction, guarded on the next due time it read. A run that is written has already moved the schedule on; a run that is not written has not.

**The database says once as well.** A unique index on the schedule and the due time refuses a second scheduled run for one due time, so even a fault in the code above cannot run it twice.

**Late, with the reason.** A run that starts more than `agents.schedule_late_after` after its due time, or that stands for more than one, is marked late: `stopped` when the due time passed before this start of the server (Keystone was not running), `busy` otherwise (the run before was still going, or too many tasks were open).

**A due time waits for the run before.** While a schedule's last run is under way, a due time that comes is not run beside it. When the run ends, one run starts for every due time that came meanwhile, late, as `busy`.

**A run is started in steps that can be repeated.** The row first (`starting`), then the chat, noted on the row, then the task, noted on the row (`started`). A run left `starting` by a stop is begun again at the next look, from the step it reached.

## Why

- A brief for "this morning" that the server missed is worth one run when it is back, not three in a row saying the same thing and paying three times. New Keystone's routines did the same: the latest slot at or before now, run once, marked as a catch-up.
- Moving the next due time in the transaction that writes the run is what makes "once" hold across a crash at any moment: there is no window in which a run exists and the schedule still points at the time it ran for, or the other way round.
- Saying why a run was late is what the mockup shows ("Missed at 7:00 because the computer was off. Ran once when it was back."), and the person can tell an outage from a long run.
- Two runs of one schedule at once would race for the same work; waiting folds the time that came into one late run, as an outage does.

## What it costs

- A run after a long stop does the latest due time's work only: an hourly schedule stopped for a day runs once, not 24 times. That is the plan's rule.
- A chat made for a run, and the server stopped before the run noted it, is made again at the next start; the first stays, empty.
