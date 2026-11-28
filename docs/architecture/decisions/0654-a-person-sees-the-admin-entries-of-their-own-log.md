# 0654. A person sees the admin entries of their own log in Settings, with the admin account's name

November 27, 2026 · decided in P15-01

## The question

The spec says every admin read or change of a person is written in that person's audit log, and the person can see these entries in their own Settings; the admin cannot read the person's log. What does the person's route return, and how does the person know which admin it was, when the admin account is in a database the person's routes do not open?

## The decision

- **A route of the person's own**: `GET /api/v1/audit/admin`, behind the person's session like `GET /api/v1/audit`, with `after` and `limit`, answering the same shape (`AuditReply`). It lists only the entries whose actor starts with `admin:`, through a new function in `internal/audit`, `ListByAdmin`, in a file of its own.
- **The name travels in the entry.** `adminview` writes the admin account's name, as it is at the time, in the entry's `sent` field (`{"by": "...", "read": [...]}`), beside the account's id in the actor. The person's route opens nothing but the person's own database.
- **The screen**: Settings → Admin access folds each admin call's two entries into one row, newest first: who, what was read (in words), when, through which route, and whether it went through. There is no mockup, so it is provisional.
- **The admin cannot read it**: no admin route lists a person's log, and the admin isolation check fails if `internal/adminview` or `internal/adminapi` calls `audit.List` or `audit.ListByAdmin`.

## Why

- Filtering by actor in the database keeps paging honest: a page of admin entries is a page, however many of the person's own calls lie between.
- A name in the entry, written once, is what the person needs to recognise the admin, and keeps the person's route out of the admin database. A renamed or removed account leaves its old entries as they were, which is what an audit log should do.

## What it costs

- The name is copied into every entry an admin read writes; it is short.
- Entries an admin read wrote before this item (the first pass's) have no name; the screen says "An admin".
