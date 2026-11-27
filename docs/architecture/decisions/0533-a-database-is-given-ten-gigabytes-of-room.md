# 0533. A person's database is given ten gigabytes of room, from the load test's measured size

November 26, 2026 · decided in P13-03

## The question

Operations shows a person's database, files and artifacts each against a room: `operations.database_room_mb`, `operations.files_room_mb` and `operations.artifacts_room_mb`. P11-03 shipped them as 2 GB, 20 GB and 5 GB, as guesses, to stand until the load test measured real sizes (decision [0460](0460-the-disk-is-the-volume-and-each-person-sees-their-own-folder.md), and the owner's question in `STATE.md`). The load test (P13-03) has measured. Are the three right?

## What was measured

The full run writes three years for one person: 5,001 chats, 102,416 turns, 204,832 messages, about 93 turns a day, on Windows 11.

| | Measured | Shipped room |
| --- | --- | --- |
| The database, `keystone.db` | 7.16 GB | 2 GB |
| The files folder | 43 MB, for 200 uploads of 200 KB on average | 20 GB |
| The artifacts folder | 3.3 MB, for 125 artifacts in two versions | 5 GB |

The database is about 70 KB a turn, and little of that is the chat:

| Table | Share | What it holds |
| --- | --- | --- |
| `audit_log` | 58% | Every model call's whole request: the tools' definitions and the chat so far, again for each turn. |
| `turn_events` | 25% | Every streamed piece of every reply, about 150 rows a turn. |
| `turn_contexts` | 6% | The record of what each call was built from. |
| `messages` and their search index | 8% | The chats themselves. |

## The decision

- **`operations.database_room_mb` goes from 2048 to 10240.** At the measured 70 KB a turn, 2 GB is 30,000 turns: eleven months of the load's person, or three years at 28 turns a day. Nothing removes a row, so a person who uses Keystone daily would see their database as "watch" and then "full" within a year or two, with nothing to do about it. 10 GB holds the three measured years at 72% of the room, under the watch level of 80%.
- **`operations.files_room_mb` stays at 20480 and `operations.artifacts_room_mb` at 5120.** The measured folders are small, but their sizes are the profile's own guesses (how often a person uploads, and how large), not something the load test found out. 20 GB is 4,000 uploads at the 5 MB limit. Neither room is shown wrong.

The rooms are what Operations measures against and warns at. They stop nothing: only the disk does.

## Why

- A room is "the size it is planned to grow to". A plan the first measured profile passes three times over is not a plan.
- The other way to make 2 GB right is to make the database smaller: remove a turn's events once it has ended, or keep a call's request in a smaller form. Both are decisions about what Keystone keeps, the second about the audit log, and neither is the load test's to make. They are owner questions in the journal (core 014). If either is taken, this room can come down again.

## What it costs

- A person's database may now grow to 8 GB before Operations says a word. On the machine Keystone runs on that is under 2% of the disk, and the disk is a limit of its own.
- Backup and restore read the whole database (`docs/architecture/backup.md`); a 7 GB database is a 7 GB backup before sealing.
- `TestSettingsAreHeldToTheirBounds` in `internal/operations` names the default, and names the new one.
