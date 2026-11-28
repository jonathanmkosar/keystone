# The admin account

Folders: `internal/adminaccount`, `internal/adminview`, `internal/adminapi`, `tools/admin`, with the admin door in `internal/api/admin.go`, a person's view of it in `internal/api/adminentries.go` and `internal/audit/byadmin.go`, and the check in `tools/verify/adminisolation.go`. Screens: `web/app/src/AdminApp.tsx` (`/admin/sign-in`, `/admin`) and `AdminAccessSettings.tsx` (Settings → Admin access).

P15-01. The design is `docs/plan/SPEC-router-and-admin.md`, Part 2, "The admin account" and "What an admin never sees", on the owner's answers of November 27, 2026: **A1**, an admin is a separate account, and signing in as one shows only admin controls; **A2**, an admin sees another person's numbers and settings, never their data or their logs.

The first pass made an admin a person with a mark in `keystone.json` and a step-up (decisions 0640 to 0643, journal `core-015`). This pass replaced it: decisions [0650](decisions/0650-an-admin-is-a-separate-account-in-its-own-database.md) to [0655](decisions/0655-admin-isolation-holds-the-separate-account.md).

## What an admin is

A separate account, kept apart from people in its own database, `data/admin/admin.db`. An admin account has no tenant and no tenant database: no chats, no files, no models, nothing of a person's to show. The owner has two sign-ins, his own person and his admin account, and can be signed in as both in one browser.

`internal/adminaccount` owns the database, with migrations of its own (`internal/adminaccount/migrations/`):

| Table | What it holds |
| --- | --- |
| `accounts` | id (16 hex), name (unique in any case), when added, last sign-in |
| `passwords` | one per account: the hash, its salt and iterations, the count of wrong tries and the last one |
| `sessions` | the SHA-256 of each session's token, the method, when made, when it expires, when last used |
| `passkeys` | an account's passkeys: credential id, public key, use count, name |
| `challenges` | passkey prompts waiting for their answer; a prompt to add names its account |
| `admin_audit_log` | every admin call and every admin sign-in, in the shape of a person's audit log; append-only by triggers |

It is opened per use (`adminaccount.Open(dataDir)`) and closed after, as a person's database is, so the command line and a running server share it. It is not encrypted at rest: it holds no person's data (P15-06 seals people's).

## Making an account

    go run ./tools/admin add "Admin Name"
    go run ./tools/admin password <account-id>      (one line on standard input)
    go run ./tools/admin list
    go run ./tools/admin remove <account-id> [--yes]

The command line is the only way to make or remove an account (decision 0650). `password` ends every session the account had. `remove` ends them at once, keeps the account's entries in the admin audit log, and refuses the last account without `--yes`. The `admin isolation` check fails if any code but `internal/adminaccount` and `tools/admin` calls `AddAccount` or `RemoveAccount`.

## Signing in

At `/admin/sign-in`, with the account's name or id and its password, or a passkey (behind `signin.passkeys`, off as shipped). The rules are a person's, through `internal/signin`'s own code (`signin/shared.go`): the same hashing, the same count of wrong tries and wait, the same passkey checks. An account that is not there takes as long to refuse as a wrong password. A person's door does not open an admin account, and the admin's door does not open a person.

- **The token** is `ksa_` and 32 random bytes as hex, kept only as a hash, and given in the cookie `keystone_admin`, path `/api/v1/admin/`, `HttpOnly`, `SameSite=Strict`, never in a body.
- **A session lasts `admin.session_for`** and ends after **`admin.idle_for`** without a call (decision [0652](decisions/0652-admin-sessions-last-admin-session-for-and-end-after-admin-idle-for.md)). Every call moves its last use.
- **Every sign-in, good or refused, is in the admin audit log**, with the actor `admin:<account id>`, or `admin` when the account is not known. A sign-in that cannot be recorded does not stand.

## The door

The API shell tells the two kinds of token apart by their text, before anything is read (decision [0651](decisions/0651-the-door-tells-an-admins-token-from-a-persons-by-its-form.md)):

| The call | Answer |
| --- | --- |
| An admin's token on an admin route | the route runs, with `api.Call.Admin` and `Call.AdminSession`, and no `Call.Store`; in the admin audit log before and after |
| An admin's token on a person's route (in either cookie) | 403 `admin_account`, in the admin audit log |
| A person's token on an admin route, or any admin path | 403 `not_admin`, in the person's own audit log |
| A person's token in the admin cookie | 401 `no_session`, written nowhere |
| No session, or an ended one | 401 `no_session`, written nowhere |

Every route under `/api/v1/admin/` is marked `Admin` and no other route is (`api.Check`); the admin's two sign-in routes are doors as well, listed in `adminDoorRoutes` and in `doorRoutes` in `tools/verify`.

## What an admin never sees

`internal/adminview` is the only code the admin routes use to read a person (decision [0644](decisions/0644-adminview-is-the-only-way-to-another-person-and-writes-their-log-first.md)). `internal/app` makes one `adminview.View` over the install and hands it to `internal/adminapi`, which is never given the install and does not import `internal/tenant`.

Every function of the view takes an `adminview.Asker` (the admin account's id, the request id and the route) and refuses unless the account is in the admin database now, the request id is there and the route is an admin route. It returns numbers and settings only:

- `People`: each person's id, name, when added and last sign-in, oldest first.

**Both logs.** Every read or change of a person is written in that person's own audit log before it is made, with the actor `admin:<account id>`, the admin call's request id and route, the outcome `started`, and in `sent` the account's name and what is about to be read (`{"by":"Admin Name","read":["last_sign_in"]}`); then a second entry with `ok` or `error`. If the first cannot be written, nothing of theirs is read. A change (P15-03) records the old and the new value in the same field (`adminview.Touch`). The admin's own log has the call already, written by the door with the same request id.

**The person sees it.** `GET /api/v1/audit/admin` lists the entries admin accounts left in the signed-in person's own log (`audit.ListByAdmin`), and Settings → Admin access folds each call's two entries into one row: who, what was read, when, through which route, and whether it went through (decision [0654](decisions/0654-a-person-sees-the-admin-entries-of-their-own-log.md)).

**The admin cannot read the person's log**: no admin route lists one, and the check fails if `adminview` or `adminapi` calls `audit.List` or `audit.ListByAdmin`. The server's own log carries no person's content, as before; an admin call's line ends `admin=<account id>`.

**Last sign-in** is kept in each person's `tenant_meta` as `last_sign_in`, written whenever `signin` opens a session (decision [0646](decisions/0646-the-first-admin-read-is-the-people-list-with-last-sign-in.md)).

## Routes

| Route | Door | What it does |
| --- | --- | --- |
| `POST /api/v1/admin/session` | none (a door) | Sign in: `{"account": name or id, "password": ...}` or `{"passkey": <PublicKeyCredential.toJSON()>}`. Answers the account and the session (`expires`, `idle_until`) and sets the admin cookie. 401 `refused`, 429 `wait` with `Retry-After`, 400 `passkeys_off`, 400 `bad_request` for both or a malformed answer. |
| `POST /api/v1/admin/session/passkey` | none (a door) | A passkey prompt for signing in, naming no account. 400 `passkeys_off`. |
| `GET /api/v1/admin/session` | admin | Which account is signed in, and until when. |
| `DELETE /api/v1/admin/session` | admin | Sign out and forget the cookie. |
| `GET /api/v1/admin/people` | admin | Every person: id, name, added, last sign-in. Each person's log records the read. P15-02 adds the numbers to the same rows. |
| `GET /api/v1/admin/passkeys` | admin | The account's own passkeys. |
| `POST /api/v1/admin/passkeys/prompt` | admin | A prompt to add a passkey, with the user and the algorithms for `parseCreationOptionsFromJSON()`. 400 `passkeys_off`. |
| `POST /api/v1/admin/passkeys` | admin | Add the passkey the browser made: `name`, `rawId`, `clientDataJSON`, `authenticatorData`, `publicKey`, each base64url. |
| `DELETE /api/v1/admin/passkeys/{id}` | admin | Remove one of the account's passkeys. |
| `GET /api/v1/audit/admin` | person | The admin entries of the person's own audit log, with `after` and `limit`. |

The shapes are in `docs/api/openapi.json`, where each admin route is marked `x-keystone-admin` and carries the `admin` cookie scheme.

P15-02's routes (one person, adding a person, setup codes, disable and enable, a person's usage, the install, and the door `POST /api/v1/setup`) are in [people.md](people.md).

## Screens

- `/admin/sign-in`: the account's name and password. `/admin`: who is signed in, when the session ends, and sign out. The admin screens for people, the install and settings are P15-02 to P15-04. A path under `/admin` is drawn by `AdminApp` in place of the person's app (`main.tsx`), so the admin page never asks who the person is.
- Settings → Admin access, for a person. There is no mockup for either, so both are provisional.

## Settings

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `admin.session_for` | duration | 8h | install | How long an admin session lasts. |
| `admin.idle_for` | duration | 30m | install | How long an admin session may go unused before it ends. |

The password and passkey settings are the `signin.` ones. `admin.setup_code_for` and the five `views.` settings in the spec's table belong to P15-02 and P15-04. The first pass's `admin.step_up_for` is gone.

## Removed from the first pass

The mark (`tenant.Tenant.Admin`, `Install.SetAdmin`, `IsAdmin`, `Marks`), `tools/tenant admin`, the step-up (`signin.StepUp`, `BeginPasskeyStepUp`, `PasskeyStepUp`, `EndStepUp`, `Session.AdminUntil`, the three step-up routes, `api.Route.StepUp`, 401 `step_up`), `admin.step_up_for`, and `admin` and `session.admin_until` in a person's session answer. The step-up's two migrations stay as they ran; `20261127T170000_admin_step_up_removed.sql` drops `admin_until` and the `step-up` prompt purpose. An old `keystone.json` with the mark loads, and the next save drops it (decision [0653](decisions/0653-the-mark-and-the-step-up-are-taken-back-by-a-forward-migration.md)).

## The admin isolation check

`tools/verify/adminisolation.go`, the standing check `admin isolation` (decision [0655](decisions/0655-admin-isolation-holds-the-separate-account.md)):

- **The imports.** `go list -deps` of `internal/adminview`, `internal/adminapi` and `internal/adminaccount` reaches none of the packages that hold a person's content (`adminMayNotReach`), naming the chain of imports on a failure. `internal/adminapi` does not import `internal/tenant`.
- **No log reads.** `adminview` and `adminapi` never call `audit.List` or `audit.ListByAdmin`.
- **One maker of accounts.** Only `internal/adminaccount` and `tools/admin` call `AddAccount` and `RemoveAccount`.
- **No mark.** `tenant.Tenant` has no field, or JSON name, with "admin" in it.
- **No chat, file or model route.** No admin route's path has a part named chats, messages, turns, files, uploads, artifacts, models, memory, projects, share, shared, inspector, recap, research, subagents, visuals, browser or connectors.
- **The walk**, on a throwaway folder with two people and an admin account, with the real handler: each door opens its own kind of account alone; a person's token is refused on every admin route (403 `not_admin`, in their own log alone; 401 in the admin cookie, written nowhere); the admin's token is refused on every person's route in either cookie (403 `admin_account`, in the admin log alone); every admin route the admin calls is in the admin log, before and after, and any entry it left in a person's log is the admin's own pair for the same route; the people read is in both people's logs with the admin's name, and the person sees it; a person's own call is in no admin reply; a signed-out session, and a session whose account another process removed, are refused at once; and no admin token is in a reply's body or the server's log.

P15-05 adds the drill: marker words in a second person's chats, titles, files, memory and traces, looked for in every admin reply and in the server's log.

## Checked by

- `go test ./internal/adminaccount`: accounts and their names; password sign-in by name or id; wrong passwords wait; the token's form; `admin.session_for` and `admin.idle_for`; sign-out, a new password and a removed account (from a second process) end sessions; the admin audit log is scrubbed and append-only; passkeys added and used by their own account alone, once per prompt, off as shipped.
- `go test ./internal/api`: the door with routes of the test's own: an admin route runs for an admin alone, with no store, in the admin log alone; a person's token refused on every admin path; an admin's token refused on every person's route in either cookie; both cookies in one browser; a session that is not there; `api.Check`; the cookie; a person's admin entries.
- `go test ./internal/adminview`: the people list, its two entries in each person's log with the name, and nothing else in a person's row; refusals for a person's id, a bad asker and a removed account, writing nothing.
- `go test ./internal/adminapi`: sign-in, its refusals and its wait, each in the admin log without the password; the session and sign-out; people in both logs and seen by the person; every admin route refused to a person; passkeys added, used and removed.
- `go test ./internal/tenant`: an old `keystone.json` with the mark loads and the next save drops it; the step-up's column and prompt purpose are gone.
- `go test ./tools/admin`: the command, and the last account kept without `--yes`.
- The `admin isolation` check in `verify`.

## Not here yet

- Built since by P15-02, in [people.md](people.md): the people and install numbers through `internal/figures`, adding a person with a setup code, `/setup`, disabling a person, and the disk moved from a person's Operations to the admin account.
- Settings from the browser, install settings in `keystone.json`, and a change's old and new value in both logs: P15-03.
- Views and view sets: P15-04.
- The marker-word drill: P15-05.
- The admin screens beyond sign-in: the interface halves of P15-02 to P15-04.
- Passkeys in a real browser: proven against the test authenticator only, as a person's are.
