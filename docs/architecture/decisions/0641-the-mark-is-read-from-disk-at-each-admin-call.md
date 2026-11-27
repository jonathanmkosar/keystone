# 0641. The mark is read from keystone.json on disk at each admin call, every save keeps it, and no backup carries it

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A1

## The question

The server reads `keystone.json` once, when it starts (`api.md`, "Not here yet"). The mark is changed by the command line, in another process. A mark taken away must not go on opening the admin area until the next restart, and a server that saves the registry for another reason must not write an old mark back. And a backup is one tenant in one file: should the mark travel with it?

## The decision

- **Read at each admin call.** `tenant.Install.IsAdmin(id)` reads `keystone.json` from disk, every time. The API shell calls it for every request to an admin path, and `adminview` again before any read of another person. A mark set or taken away from the command line counts at the next call, with no restart.
- **Every save keeps the marks on disk.** `Install.save` reads the marks from the file and puts them on the tenants it is about to write, so a save made by a server that started before a change (a new tenant, a restore, a keeper written on first use) cannot undo it. `SetAdmin` is the one writer that changes a mark, and it reads the file first as well.
- **A backup carries no mark, and a restore grants none.** `Install.Backup` writes the tenant's entry with `admin` false. A restore keeps whatever mark the install already has for that id, false for a new one, because the save above takes the marks from disk.
- `Install.Tenants()` still returns the list the server read at its start. Its `Admin` field can be old; code that decides anything by the mark calls `IsAdmin` or `Marks`.

## Why

- Taking the mark away is a security change and must work at once. Reading a small file on each admin call costs a fraction of a millisecond beside opening the person's store, which every call already does (decision 0040).
- The mark is install data. A person's backup is theirs and might be restored on another install, where they are not its admin.

## What it costs

- Two processes that change the registry at the same moment can still lose one change; the command line and the server rarely both write, and the window is the time it takes to write one small file. It was so before this item.
- A tenant the command line adds while the server runs is still not served until a restart, and a save by the server writes only the tenants it knows: such a tenant would be dropped from the file. This was so before; P15-02, which adds people from the server, must merge the list from disk the same way the marks are.
