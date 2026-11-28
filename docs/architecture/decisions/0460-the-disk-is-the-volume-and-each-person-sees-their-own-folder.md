# 0460. The disk is the volume, and each person sees their own folder

November 26, 2026 · decided in P11-03 · changed by decision [0701](0701-the-disk-is-shown-only-in-the-admin-account.md): the disk is shown only in the admin account

## The question

Operations shows where the install is running out of room, and the item names the data folder's disk use against the free space on its volume. The data folder holds every person's folder. Fixed rule 1 says a tenant never reads another tenant's data, and the brief says install-wide facts such as the disk are shown to every person as install facts, with nothing from another tenant. What may one person be told about the disk?

## The decision

Two kinds of limit, never mixed:

- **The disk** is the volume that holds the data folder: its used space (its size less what this process may still write) against its size, read from the operating system for the folder of the person's own database (`GetDiskFreeSpaceEx` on Windows, `statfs` on Linux, macOS and FreeBSD, and "not known" elsewhere). It is marked `install` and is the same for every person.
- **The person's own folder** is three limits: their database (the file and its write-ahead log), their `files/` and their `artifacts/`, each against a room set by the admin (`operations.database_room_mb`, `operations.files_room_mb`, `operations.artifacts_room_mb`). They are read through the person's own store: the database's path from SQLite itself, and the two folders through the store's `os.Root` handles, which cannot leave the folder.

The data folder's own total is never shown.

## Why

- With two people on an install, the data folder's total less my own folder is the size of the other person's. A size is little, but it is read from another tenant's folder, and the rule has no exception for little.
- The volume's use counts everything on the disk, Keystone or not, so it says nothing of any one person, and it is what runs out first: a full disk stops every write, the other person's included. That is the fact a person needs.
- A room for each folder gives a share and a trend where the free space alone would not: 6 GB of files means nothing until it is set against what was planned.
- The disk is read from the folder of the person's own database, so nothing outside the person's store is opened to find the volume.

## What it costs

- The admin learns the data folder's total from the operating system, not from Keystone. There is no admin view yet.
- The rooms are guesses until the load test (P13) measures a database's size: 2 GB for a database follows the mockup's "load-tested size".
- The disk snapshot is kept in each person's own database, so it is stored once for each person. It is a few rows an hour.
