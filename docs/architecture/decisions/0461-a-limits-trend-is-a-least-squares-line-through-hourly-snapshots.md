# 0461. A limit's trend is a least-squares line through hourly snapshots in the tenant's database

November 26, 2026 · decided in P11-03

## The question

Each limit on Operations shows a trend: how fast it grows and when it would fill. A limit is read now, from the store and the volume. Where does its past come from, how often, and how is a rate drawn from it?

## The decision

- **Snapshots, one table per tenant.** `operations_snapshots` (time, limit, used, of) in each person's own database, added by migration `20261126T001900_operations.sql`. The cap, the memory reserve, the disk, the database, the files and the artifacts are recorded; a limit that does not read is not.
- **A sampler under the turn runner.** It starts with the server, after the scheduler, samples every tenant at once and then every `operations.snapshot_every` (an hour), skips a tenant whose newest snapshot is younger than that, and removes snapshots older than `operations.snapshot_keep` (35 days). Closing the turn runner stops it, as it stops the scheduler.
- **The rate is a least-squares line** through the snapshots within `operations.trend_window` (a week) and the reading now, with time in days. Its slope is the growth a day; the fill date is when the line meets the limit; for the cap and the reserve, whose month starts them again, the trend reaches back no further than the month and says what the line reaches by the first of next month.
- **A run's budget has no snapshots.** A run lasts minutes; its usage records, added up in order, are its points.

## Why

- A limit's past is not anywhere else. The usage records give the cap's past, but not the disk's, the database's or a folder's, and one way for every limit keeps the trend's code one function.
- In the tenant's own database, the snapshots go with the person: a backup carries them, a restore brings them, and deleting a person removes them. Nothing is kept outside a tenant's folder.
- Skipping a tenant sampled within the interval means a restart, or several in an hour, takes no extra snapshot, and the sampler needs no clock of its own beyond the newest row.
- A line through every point is steadier than the first and last points alone: one odd reading, such as the disk during a large copy, moves it little. It is a few lines of arithmetic, with no library.
- Hourly is enough for limits that move over days and weeks, and a week of hourly rows is 1,008 for the six limits.

## What it costs

- A trend needs history: a new install says "too early to tell" until its second snapshot, and its rate is rough for the first day.
- A straight line misreads growth that speeds up or comes in steps. The fill date is "at this rate", and says so.
- The disk is the same for every person but is recorded in each person's database (decision [0460](0460-the-disk-is-the-volume-and-each-person-sees-their-own-folder.md)).
