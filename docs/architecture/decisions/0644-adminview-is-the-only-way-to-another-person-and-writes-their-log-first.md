# 0644. adminview is the only way an admin route reaches another person, and writes in that person's log before it reads

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A2

## The question

Decision 0040 gives a route's handler one store, its caller's, and nothing to open another with. An admin route has to read other people's numbers and settings. How is that allowed without opening a way to their chats, and how does each person learn that an admin looked?

## The decision

- **`internal/adminview` holds the install and nothing else does.** `adminview.New(install)` is made in `internal/app`, and the admin routes' package, `internal/adminapi`, is given the `*adminview.View` and never the install. `adminapi` does not import `internal/tenant`, and the `admin isolation` check fails if it does.
- **It returns numbers and settings only.** Today: each person's id, name, mark, when they were added and their last sign-in. Its imports are held by the check (decision 0645), so it cannot reach the packages that hold chats, titles, messages, files, memory or traces.
- **Each read of another person is written in their log first.** Before it opens anything of theirs, `adminview` appends an entry to that person's audit log with the actor `admin:<the admin's tenant id>`, the admin call's request id and route, the outcome `started`, and what it is about to read in `sent`; after the read, a second entry with `ok` or `error`. If the first cannot be written, nothing of theirs is read and the call fails. A change (P15-03) will record the old and the new value the same way.
- **It asks for the mark again.** Every function takes an `adminview.Asker` (the admin's id, the request id and the route) and refuses unless `tenant.Install.IsAdmin` says that id is marked now.
- The admin's own facts are read from their own store with no second entry: their own log already holds the call.

## Why

- Fixed rule 1 says no person sees another's data. An admin is a person. Making the one door narrow, and checking it by imports, keeps a later item from widening it by accident.
- Writing first, in the same shape as `audit.Wrap`'s entries, means there is no read without a record (the spirit of fixed rule 4), and the person sees it in their own audit log under the admin's name.

## What it costs

- Every people list writes two entries in every other person's log. That is what the spec asks.
- Packages that already sum a person's numbers (`usagereport`, `operations`, `jobs`, `schedules`) reach forbidden packages through their own imports, so `adminview` cannot call them. P15-02 must read those numbers through narrow functions of their own, or split those packages.
