# 0655. The admin isolation check holds the separate account: both tokens refused on the other's routes, both logs, no mark

November 27, 2026 · decided in P15-01 · changes decision [0645](0645-admin-isolation-starts-with-the-imports-and-the-door.md)

## The question

The `admin isolation` check was built for the mark and the step-up. With a separate admin account (decisions 0650 and 0651), what does it hold, so that it is at least as strict as before and proves the item's proof?

## The decision

The check, in `tools/verify/adminisolation.go`, now holds:

- **The imports**, as before, for three packages: `internal/adminview`, `internal/adminapi` and the new `internal/adminaccount` reach none of the packages that hold a person's content, directly or through another, and `internal/adminapi` does not import `internal/tenant`.
- **No log reads**: no Go file in `internal/adminview` or `internal/adminapi` calls `audit.List` or `audit.ListByAdmin`.
- **One maker of accounts**: no Go file outside `internal/adminaccount` and `tools/admin` calls `AddAccount` or `RemoveAccount` (it replaces "one writer of the mark").
- **No mark**: `tenant.Tenant` has no field, or JSON name, with "admin" in it.
- **No chat, file or model route**: no admin route's path has a part named chats, messages, turns, files, uploads, artifacts, models, memory, projects, share, shared, inspector, recap, research, subagents, visuals, browser or connectors.
- **The admin doors are the listed ones**, `adminDoorRoutes`, and each is also in `doorRoutes` in `routes.go`.
- **A walk with the real handler**, on a throwaway folder with two people and one admin account made as the command line makes it:
  1. Each door opens its own kind of account alone; a wrong admin password is refused and in the admin log; the admin's cookie is path-scoped, `HttpOnly` and strict, and its token is `ksa_` and 64 hex, never in the body.
  2. A person's token on every admin route and on an admin path that is no route: 403 `not_admin`, two entries in their own log, nothing in any other; in the admin cookie, 401 and nothing written.
  3. The admin's token on every person's route and on a path that is no route, in either cookie: 403 `admin_account`, two entries in the admin log, nothing in any person's.
  4. The admin on every admin route: never refused for who it is; each call two entries in the admin log; any entry in a person's log for it the admin's own pair for the same route.
  5. The people read in the admin log and in both people's logs with the admin's name, and the person sees it through `GET /api/v1/audit/admin`; a person's own call's request id appears in no admin reply.
  6. Signed out, and then a fresh session whose account is removed from a second process: every admin route 401 at once, nothing written.
  7. No admin token in any reply's body or in the server's log.

## Why

- Every refusal the first pass's walk proved has a counterpart here (a person refused on admin routes, a closed session refused, an account taken away by the command line refused at once), and the walk now proves the other direction too, which the first pass had no way to.
- The static parts are cheap and fail with a message that says what to change.

## What it costs

- The walk signs in four times under the shipped hashing settings and calls every route two or three times; it adds about ten seconds to `verify`.
- P15-05 still adds the marker-word drill: marker words in a second person's content looked for in every admin reply and in the server's log.
