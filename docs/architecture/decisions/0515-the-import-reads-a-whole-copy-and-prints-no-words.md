# 0515. The import reads a whole copy of the database, and prints no word of it

November 26, 2026 · decided in P12-03

## The question

New Keystone keeps `workspace.sqlite` in write-ahead mode and may be running when the owner wants to import. The import is run by the owner, or for him by an integrating session whose transcript is kept. What file does the import read, and what may it print?

## The decision

- **The import reads a copy, never the live file.** `OpenSource` refuses a file with a write-ahead log or a journal beside it that holds anything, and says how to make a copy. It opens the copy read-only and immutable, so SQLite writes nothing to it or beside it, and it first runs SQLite's quick check and looks for New Keystone's own columns.
- **Keystone makes the copy itself**: `tools/tenant import-copy <workspace.sqlite> <new file>` opens the database read-only and runs `VACUUM INTO`, which writes everything the database and its log hold at one moment into one new file, then reads the copy back. It works while New Keystone runs and never replaces a file.
- **Neither the dry run nor the import prints a title, a routine's name or a word of a message.** They print counts, the names of models and tables, dates, the reasons things are left out, and the first characters of the copy's SHA-256. The audit entries hold the same and no more. When a copy holds several workspaces, their names and turn counts are printed so one can be named.
- **The record keeps which copy was read** as its SHA-256, and no path.

## Why

- In write-ahead mode the newest turns are in `workspace.sqlite-wal` until a checkpoint moves them. A plain copy of the one file can lack them, and a copy made while a write is under way can be torn. Reading the live file in place would be correct but would have Keystone's import hold read locks on another app's database for as long as it runs, and tie what is imported to a moment nobody chose.
- Asking the owner to copy the file safely needs a tool that knows SQLite. Python or the `sqlite3` program may not be on the machine; Keystone already carries the same SQLite driver. One command of Keystone's own makes the step the same everywhere and lets the import refuse anything else.
- Opening the copy as immutable is what makes "read-only" true on Windows, where SQLite otherwise makes a shared-memory file beside any database in write-ahead mode it reads.
- The owner's old chats are private, and this repo and its build sessions are not the place for them. A command that prints only counts can be run by anyone, pasted into a journal, or read by an agent, and nothing of his leaves the database. A test reads both reports for every prompt, reply, title and routine name of its fixture.
- A path can hold a person's name. A hash says which copy it was and nothing else.

## What it costs

- One more step before the import, and a second file with the person's chats in plain text, which the runbook tells him to delete afterwards.
- The dry run cannot show the titles the chats would get. The owner sees them after the import, where a chat can be renamed or deleted.
- `import-copy` does read the live database, through the shared-memory file and the log New Keystone keeps beside it while it runs, as any second reader of a database in write-ahead mode does. A database that is open nowhere has neither file, and is read as immutable so that none is made.
