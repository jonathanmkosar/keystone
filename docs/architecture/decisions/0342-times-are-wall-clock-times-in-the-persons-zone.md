# 0342. A schedule's times are wall-clock times in the person's zone, with rules for the changes of the clocks

November 25, 2026 · decided in P8-03

## The question

The item says schedules run in the tenant's time zone. Where does the zone come from, and what does a time of day mean on the two nights a year the clocks change, when a time does not happen or happens twice?

## The decision

**The zone is a person's setting**, `agents.time_zone`, an IANA name such as `Europe/Lisbon`. Empty, the default, means the zone of the computer Keystone runs on. A name that is no zone Keystone knows is said on the Scheduled screen, and the computer's zone is used until it is fixed. When the zone changes, every next due time is worked out again in the new zone from now, and nothing runs for the change.

**The zone database is compiled in** (`time/tzdata`, about 450 KB), so the binary knows every zone on Windows, which has no database of its own.

**A time of day is a wall-clock time** in the zone:

- A daily run at 07:00 stays at 07:00 across a change: the hours between two runs that day are 23 or 25.
- A time the clock skips runs as long after the change as it was after the start of the gap: 02:30 runs at 03:30 when 02:00 becomes 03:00.
- A time that happens twice runs once, at the first.
- An hourly schedule counts real hours: it runs twice in the hour the clocks repeat, and not in the hour they skip.

The rules are written out in `wallTime` and `nextHourly` and do not lean on what Go's `time.Date` happens to choose in a gap or an overlap, which its documentation leaves open.

**The API sends due times in the person's zone**, so the JSON text carries the zone's offset ("2026-11-01T07:00:00-05:00") and the screen shows the wall-clock time from the text, whatever zone the browser is in.

## Why

- A person thinks of "every weekday at 7" in their own clock, and expects 7 after the clocks change.
- A setting with an IANA name is the one form that carries the rules of the clocks; an offset would be wrong half the year. Defaulting to the computer's zone is right for one person at home and needs nothing set.
- A skipped time must still run that day; running it as far into the new hour as it was into the old one is what cron-like schedulers commonly do, and it is easy to say. Running a doubled time twice would run a daily schedule twice in a day.
- An hourly schedule is about the passing of hours, not the clock's face.
- Without the compiled-in database, `time.LoadLocation` fails on a Windows machine with no Go installed, which is the owner's.

## What it costs

- The binary is about 450 KB larger.
- The computer's own zone on Windows has no IANA name, so the screen calls it "This computer's time zone" with its abbreviation.
- A person who travels sets the zone by hand.
