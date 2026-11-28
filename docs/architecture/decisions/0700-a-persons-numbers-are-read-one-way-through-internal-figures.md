# 0700. A person's numbers are read one way, through internal/figures, for their own page and for the admin

November 28, 2026 · decided in P15-02 · keeps decision [0462](0462-operations-reads-each-part-and-sums-nothing-a-second-way.md) and [0644](0644-adminview-is-the-only-way-to-another-person-and-writes-their-log-first.md)

## The question

The admin's list of people shows each person's spend against the cap, the size of their database, files and artifacts, failed and stuck tasks, schedules, model health and the limits at their watch level, and the proof asks that these equal the person's own Usage and Operations figures. Operations and the usage report already sum them. But `internal/adminview` may not reach any package that holds a person's words (decision 0655), and `operations`, `usagereport`, `jobs` and `schedules` each do, through their own imports: chat titles, task titles, prompts, error words. How does the admin read the same numbers without reaching the words?

## The decision

A new leaf package, `internal/figures`, holds the part of Operations that is only numbers, and Operations is built on it:

- the report's settings and their bounds (`Settings`, `ReadSettings`), the limits' keys, units, scopes and states;
- `Standing`, the month against the cap and the memory reserve, read as the usage report reads them;
- `Measure`, where the cap, the reserve, the database, the files and the artifacts stand now, and `Judge`, a limit's share and state;
- `DatabaseFile`, `DatabaseSize`, `FolderSize`, `Volume`, `WindowRecords`, `HealthState`, `Newest`.

`internal/operations` keeps every exported name it had (as aliases and thin wrappers) and calls these, so the page and the admin read one function. `figures` imports `tenant`, `tenantsettings`, `usage`, `models` and `settings`, and nothing that holds content; the `admin isolation` check holds it with the admin packages.

What Operations reads through a package that holds words, `figures` counts from the same rows with a narrow query that selects no text: the open tasks' request ids and budgets (`RunBudgets`), the failed and stuck tasks (`ReadJobs`), the schedules (`Schedules`), a period's calls by day and model (`Usage`), failures by kind (`Failures`) and storage over time (`Storage`). A test holds each to Operations' and the usage report's own on the same data, and the install's to the sum of each person's.

## Why

- One function for a limit is what decision 0462 asked of Operations, and the admin is one more reader of it: the person and the admin cannot disagree about the cap or the database.
- The narrow queries read columns that are numbers or ids, so nothing in them could carry a person's words to an admin reply, which the isolation check could not prove of `operations` itself.
- Splitting `operations` along the line between numbers and words keeps its routes and screens as they were; the alternative, handing `adminview` a function from `internal/app` that calls `operations`, would put content packages behind the admin's door with nothing to check it.

## What it costs

- The counts of tasks and schedules are a second reading of the same rows, held equal by tests rather than by construction. A change to how `jobs` stores a status must change `figures` too; the equality test fails until it does.
- `figures.Standing` repeats the usage report's cap and reserve rather than replacing them, so `internal/usagereport`, which another lane was changing at the same time, is untouched; a test holds the two equal.
