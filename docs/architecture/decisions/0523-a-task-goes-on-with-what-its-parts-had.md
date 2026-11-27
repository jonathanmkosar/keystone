# 0523. After a pause or a restart, a research task goes on with what its parts had

November 26, 2026 · decided in P8-04

## The question

The switch that pauses every agent ends sub-agents (decision 0484), and so does a restart (decision 0485): nothing waits for their results, so nothing runs again unasked. A research task does wait for its parts, and is meant to go on. When its parts were cut short, does it run them again?

## The decision

**No.** A task's own turn that was cut short, its plan or its report, is sent again. Parts that were cut short stay ended, with what they had written and the sources their recorded searches had returned, and the task goes on to its report. The report's "About this report" and the task's notice name each part that did not finish and why. The task's own chat takes at most `agents.research_max_rounds` turns.

At its budget the same holds: a part that was refused ends with what it had, and a report step that was refused is put together by Keystone from the parts' findings.

## Why

- **A part's work is spent.** Running a part again reads the same pages and pays for them twice, out of a budget that was split once. Going on with what there is keeps the task inside its budget.
- **One rule for sub-agents.** A part is a sub-agent, ended by the same supervisor and the same `Recover` as any other. A second kind that comes back would be a second set of states to get right.
- **A report that says what it lacks is useful.** The person reads which part is missing and can ask again; a task that failed outright gives them nothing for what was spent.
- **The plan and the report are cheap to send again**, and are one call each.

## What it costs

- A pause in the middle of the parts gives a thinner report than waiting would have. The person sees it in the report and the note, and starts another task if they want the rest.
- A task with every part cut short and nothing written fails with `nothing_found`.
