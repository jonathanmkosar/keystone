# The admin mark and step-up

Folders: `internal/adminview`, `internal/adminapi`, with `internal/tenant/admin.go`, `internal/signin/stepup.go`, the admin door in `internal/api/api.go`, `tools/tenant/admin.go` and `tools/verify/adminisolation.go`

P15-01. The design is `docs/plan/SPEC-router-and-admin.md`, Part 2, "The admin mark" and "Reading another person". **Provisional** on the owner's answers A1 (an admin is a person with a mark, set from the command line only, with a step-up before the admin area) and A2 (an admin sees another person's numbers and settings, never their chats, titles, messages, files, memory, traces or error words). Both are the spec's assumed answers.

## What an admin is

A person with a mark. Each tenant's entry in `keystone.json` holds `admin`, `true` or `false`. An admin signs in as themselves, with their own password or passkey, and keeps their own chats and settings like anyone else (decision [0640](decisions/0640-an-admin-is-a-person-with-a-mark-set-from-the-command-line.md)).

    go run ./tools/tenant admin <tenant-id> on
    go run ./tools/tenant admin <tenant-id> off [--yes]

The command line is the only way to change the mark. It calls `tenant.Install.SetAdmin`, which nothing else calls; no route can. Taking the mark from the install's last admin is refused with a warning unless `--yes` is given. `go run ./tools/tenant list` shows the mark in an `ADMIN` column.

**The mark is read from disk at each admin call** (decision [0641](decisions/0641-the-mark-is-read-from-disk-at-each-admin-call.md)). `Install.IsAdmin(id)` and `Install.Marks()` read `keystone.json` now, so a mark set or taken away from the command line counts in a running server at the next call, with no restart. Every save of the registry takes the marks from the file first, so a server that read the file before a change cannot write the old mark back. `Install.Tenants()` still returns the list the server read at its start, and its `Admin` field may be old: nothing decides anything by it.

A backup carries no mark, and a restore grants none: a restored tenant keeps the mark the install already had for that id, and a new one has none.

## The step-up

The admin area asks for the password or a passkey again, on the session the admin already has, and stays open for `admin.step_up_for` (decision [0643](decisions/0643-step-up-proves-the-password-again-on-the-same-session.md)).

- `signin_sessions` gains `admin_until`, empty until a step-up, and never past the session's own end. `signin.Session.AdminUntil` carries it, read with the session on every call; `Session.SteppedUp(t)` is the test.
- `signin.StepUp(store, sessionID, password, options, lasts)` checks the password through the same function as sign-in, so a wrong one counts towards the same wait and a wait refuses it unread.
- `signin.BeginPasskeyStepUp` and `signin.PasskeyStepUp` do the same with a passkey. Their prompts have the purpose `step-up`, so a sign-in answer cannot step up and a step-up answer cannot sign in. The migration `20261127T150100_step_up_challenges.sql` widens the challenges table for it. Passkeys stay behind `signin.passkeys`, off as shipped.
- `signin.EndStepUp` closes the admin area early. Signing out, or a new password, ends the session and its step-up with it.

## The admin door

Every admin route is under `/api/v1/admin/` and marked `Admin` in its row, and `api.Check` refuses a route whose mark and path disagree (decision [0642](decisions/0642-the-admin-door-is-a-mark-on-a-route-checked-by-the-shell.md)). The shell checks every request to a path under the prefix, a route or not, inside `audit.Wrap` and before the route runs:

| The caller | Answer |
| --- | --- |
| No session | 401 `no_session`, as everywhere |
| A person without the mark | 403 `not_admin` |
| An admin without a fresh step-up, on any route but the step-up's | 401 `step_up` |
| An admin with a fresh step-up | the route runs, with `api.Call.Admin` true |

A route marked `StepUp` needs the mark and not a fresh step-up. There are two, `POST /api/v1/admin/step-up` and `POST /api/v1/admin/step-up/passkey`, and `stepUpRoutes` in `tools/verify/adminisolation.go` repeats the list, as `routes.go` repeats the doors.

A client must not treat every 401 as a session that ended: `step_up` asks for the password again and keeps the session.

## Audit, in both logs

- **The admin's own log.** Every call to an admin path by a person with the mark is recorded by `audit.Wrap` with the actor `admin`, before it runs and when it ends, a refused one included. A refused call by a person without the mark is recorded in their own log with the actor `person`.
- **The other person's log.** Every read of another person through `adminview` is written in that person's log first, with the actor `admin:<the admin's tenant id>`, the admin call's request id and route, the outcome `started`, and what is about to be read in `sent` (`{"read":["last_sign_in"]}`); then a second entry with `ok` or `error`. If the first entry cannot be written, nothing of theirs is read and the call fails with 500. A change (P15-03) records the old and the new value in the same field (`adminview.Touch`).

The two logs share the request id, so a person reading their own log sees which admin looked, when and through which route.

## Reading another person

`internal/adminview` is the only code the admin routes use to read another tenant (decision [0644](decisions/0644-adminview-is-the-only-way-to-another-person-and-writes-their-log-first.md)). `internal/app` makes one `adminview.View` over the install and hands it to `internal/adminapi`, which is never given the install and does not import `internal/tenant`.

Every function of the view takes an `adminview.Asker` (the admin's tenant id, the request id and the route) and refuses unless `IsAdmin` says that id carries the mark now, the request id is there and the route is an admin route. It returns numbers and settings only:

- `People`: each person's id, name, mark, when they were added and their last sign-in, oldest first. The admin's own row is read from their own store with no second entry.

**Last sign-in** is kept in each person's `tenant_meta` as `last_sign_in`, written whenever `signin` opens a session (decision [0646](decisions/0646-the-first-admin-read-is-the-people-list-with-last-sign-in.md)). A person who has not signed in since this item shows their newest open session instead, or nothing.

## Routes

| Route | Door | What it does |
| --- | --- | --- |
| `POST /api/v1/admin/step-up` | mark | Opens the admin area with `{"password": ...}` or `{"passkey": <PublicKeyCredential.toJSON()>}`. Answers `{"admin_until": ...}`. A wrong one is 403 `refused`, a wait 429 `wait` with `Retry-After`, passkeys off 400 `passkeys_off`, both or neither 400 `bad_request`. |
| `POST /api/v1/admin/step-up/passkey` | mark | A passkey prompt for the step-up, in the shape `PublicKeyCredential.parseRequestOptionsFromJSON()` reads, with `userVerification: required`. 400 `passkeys_off` while passkeys are off. |
| `DELETE /api/v1/admin/step-up` | mark and step-up | Closes the admin area on this session. |
| `GET /api/v1/admin/people` | mark and step-up | Every person: id, name, admin, added, last sign-in. Each other person's log records the read. P15-02 adds the numbers to the same rows. |

`GET /api/v1/session` and the sign-in answer gain `admin`, whether the person carries the mark, and `session.admin_until` when a step-up is open, so the interface can show the admin area to admins alone. The admin routes check the mark themselves.

The shapes are in `docs/api/openapi.json`, where each admin route is marked `x-keystone-admin` and each step-up route `x-keystone-step-up`. No screen is built: the admin screens are P15-02 to P15-04.

## Settings

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `admin.step_up_for` | duration | 15m | install | How long the admin area stays open after a step-up, never past the session's end. |

`admin.setup_code_for` and the five `views.` settings in the spec's table belong to P15-02 and P15-04.

## The admin isolation check

`tools/verify/adminisolation.go`, the standing check `admin isolation` (decision [0645](decisions/0645-admin-isolation-starts-with-the-imports-and-the-door.md)). Its first part, built here:

- **The imports.** `go list -deps` of `internal/adminview` and of `internal/adminapi` reaches none of the packages that hold a person's content: the spec's chat, turn, context, knowledge, uploads, artifacts, projects, recap, inspector and share, and the others that hold a person's words (`adminMayNotReach` in the file). A failure names the chain of imports, such as `internal/adminview -> internal/usagereport -> internal/knowledge/memory`.
- **The install stays out of the routes.** `internal/adminapi` does not import `internal/tenant`.
- **One writer of the mark.** No Go file outside `internal/tenant` and `tools/tenant` calls `SetAdmin` (tests, and the check's own walk, left out).
- **The door, walked**, on a throwaway folder with the real handler and the shipped settings: every admin route, and a path under the prefix that is no route, refuses a person without the mark (403 `not_admin`) and an admin without a step-up (401 `step_up`, the step-up routes left out), each refusal in the caller's log with the right actor and in no other log; a wrong password at the step-up is refused and recorded; after a good one the people route answers and is in both logs, the other person's with the actor `admin:<id>`; a closed step-up is refused again; and a mark taken away by a second process refuses a fresh step-up at once. The step-up routes in the code must be the listed ones.

P15-05 adds the second part: marker words in a second person's chats, titles, files, memory and traces, looked for in every admin route's reply.

The `tenant isolation`, `calls recorded` and `no keys in output` walks reach the admin routes with no new code: they call them as a person without the mark, and each answers 403 and is recorded. The isolation drill calls them as its second person, who has no mark, and is refused the same way.

## Checked by

- `go test ./internal/tenant`: the mark is set and read; a mark set or taken away by a second process counts at once and is not undone by the first process's save; a backup carries no mark, a restore onto another install grants none, and one over a marked tenant keeps it.
- `go test ./internal/signin`: a step-up lasts its time and no longer than the session; another tenant's password, a session that is not there and tenant one's session on tenant two's store are refused; wrong step-ups count towards the sign-in's wait; a passkey step-up works once and the two prompts' purposes cannot answer each other; the last sign-in is kept across a sign-out.
- `go test ./internal/api`: the door, with routes of the test's own: refusals, codes and actors, the step-up route's exception, the session reply, and the mark taken away; `api.Check` holds the prefix and the mark together.
- `go test ./internal/adminview`: the people list and its two entries in the other person's log and none in the admin's; the view refuses a person without the mark, a bad asker and a mark taken away, and writes nothing then.
- `go test ./internal/adminapi`: the step-up by password and by passkey, its refusals and its wait, the people route in both logs, closing the admin area, and every admin route refused to a person without the mark.
- `go test ./tools/tenant`: the command, and the last admin kept without `--yes`.
- The `admin isolation` check in `verify`.

## Not here yet

- The people and install numbers, adding a person with a setup code, and `/setup`: P15-02. The packages that already sum a person's numbers (`usagereport`, `operations`, `jobs`, `schedules`) reach forbidden packages through their own imports, so `adminview` cannot call them; P15-02 reads those numbers through narrow functions or splits the packages.
- Settings from the browser, install settings stored in `keystone.json`, and a change's old and new value in both logs: P15-03.
- Views and view sets: P15-04.
- The marker-word drill: P15-05.
- The admin screens: the interface halves of P15-02 to P15-04.
