# 0652. An admin session lasts admin.session_for and ends after admin.idle_for without use

November 27, 2026 · decided in P15-01

## The question

The spec gives two settings: `admin.session_for` (8h) and `admin.idle_for` (30m), both install. How is "without use" counted, and what happens at either limit?

## The decision

- **Both are kept on the session row**: `expires_at`, set at sign-in to now plus `admin.session_for`, and `last_used_at`, moved to now at every call the admin door lets through (`adminaccount.Store.Check`).
- **A session past either is deleted and refused** at its next call, 401 `no_session`; opening a new session also deletes every session past either limit.
- **Both are read at start**, through `adminaccount.ReadOptions`, with the `signin.` settings, and handed to the shell (`api.Options.Admin`) and the admin routes. A value that is not longer than nothing stops the server from starting.
- **The sign-in and session answers say both ends**: `session.expires` and `session.idle_until`, so the admin page can show them.
- **The admin cookie's own expiry is `expires`**; the idle limit is the server's alone.

## Why

- The step-up that guarded the admin area (decision 0643) is gone; a short session, ended by disuse, stands in for it. An unlocked browser left signed in as the admin closes itself within half an hour.
- One write per admin call is nothing beside the two audit entries every admin call already writes.

## What it costs

- Every admin call writes the session row, so a read-only admin call is not read-only on the admin database.
- A clock moved back can keep a session alive longer; the clock is the machine's, as it is for every session.
