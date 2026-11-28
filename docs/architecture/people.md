# People, the install and monitoring

Folders: `internal/figures`, `internal/setupapi`, with the admin's side in `internal/adminview` (`numbers.go`, `change.go`, `usage.go`), `internal/adminapi/people.go` and `internal/adminaccount/setupcodes.go` (migration `20261128T110000_setup_codes.sql`), the disabled mark in `internal/tenant/disabled.go` and `internal/signin/disabled.go`, the registry's merge in `internal/tenant/merge.go`, and `tools/verify/people.go`. Screens: `web/app/src/AdminPeople.tsx` (People, A person, Install), `adminPeopleCalls.ts`, `admin-people.css` and `SetupPage.tsx` (`/setup`).

P15-02. The design is `docs/plan/SPEC-router-and-admin.md`, Part 2, "Disabling a person" and "People, the install and monitoring", on the owner's answers A1 and A2 (an admin is a separate account that sees numbers and settings, never data or logs) and the recommended A5 (a few people). The admin account itself is [admin.md](admin.md). Decisions [0700](decisions/0700-a-persons-numbers-are-read-one-way-through-internal-figures.md) to [0704](decisions/0704-the-servers-save-of-keystone-json-merges-the-tenant-list-from-disk.md).

## A person's numbers, one way

`internal/adminview` may not reach a package that holds a person's words, and `operations`, `usagereport`, `jobs` and `schedules` all do. `internal/figures` is the part of Operations that is only numbers, and Operations is built on it (decision 0700):

| Exported | What it is |
| --- | --- |
| `Settings`, `ReadSettings`, `ClampDuration` | Operations' per-person settings and their bounds (`operations.Settings` is an alias). |
| `Limit*`, `Unit*`, `Scope*`, `State*`, `Health*`, `Setting*` | The keys and names Operations used; `operations` keeps its own names as aliases. |
| `Standing(ctx, s, reg, month)` | `Cap` and `Reserve`: the month against the monthly cap and the memory reserve, as the usage report reads them (a test holds the two equal). |
| `Measure(ctx, s, reg, cfg, now)` | `[]Reading`: the cap, the reserve, the database, the files and the artifacts now. `operations.measure` is it. |
| `Judge(key, used, of, watch, problem)` | A limit's percent, state and whether it is crossed. `operations.judge` is it. |
| `DatabaseFile`, `DatabaseSize`, `FolderSize`, `Volume`, `ReadDisk` | Sizes on disk (sealed sizes for a sealed person) and the volume of a folder. |
| `WindowRecords`, `HealthState`, `ModelStates` | A window's usage records, a model's health, and a person's models counted by health. |
| `RunBudgets`, `Crossed` | Open tasks' budgets and spend, and the keys of the limits whose watch level is crossed, as Operations' watch route lists them. |
| `ReadJobs` (`Jobs`), `Schedules` | Tasks failed within the health window and stuck; schedules counted. |
| `Usage` (`Use`, `Total`, `Day`, `ModelUse`), `Failures`, `Storage`, `Newest` | A period's calls by day and by model, failures by kind, storage over time from Operations' snapshots, and the newest snapshot. |

It imports `tenant`, `tenantsettings`, `usage`, `models` and `settings`. The counts that Operations reads through a content package (`RunBudgets`, `ReadJobs`, `Schedules`, `Failures`) are narrow queries that select no text, and `TestTheNumbersAreEachPersonsOwnAndTheInstallsAreTheirSum` holds each to Operations' own. `figures` names the memory pass's kind and reserve setting itself (`KindMemory`, `SettingReserve`), and a test holds them to `internal/knowledge/memory`'s.

## Routes

All behind the admin door ([admin.md](admin.md)), except the setup door. Every read or change of a person goes through `adminview`, which writes the admin's pair of entries in that person's log first (decision 0644); the door writes the admin's log.

| Route | What it does |
| --- | --- |
| `GET /api/v1/admin/people` | Every person, oldest first: id, name, added, last sign-in, and their `Numbers`: `enabled` and `disabled_at`, `has_password`, `setup_until` (a code waiting), `spend` (the cap: spent, limit, warning level, state), `storage` (database, files, artifacts), `jobs` (failed, stuck), `schedules`, `models` (counts by health), `crossed` (limit keys). |
| `GET /api/v1/admin/people/{id}` | One person, as a row. |
| `POST /api/v1/admin/people` | `{"name"}`: makes the person (sealed) and a setup code. Answers `person` and `setup_code` (`code`, `expires`), shown this once. 400 for a name that is empty or longer than 100. |
| `POST /api/v1/admin/people/{id}/setup-code` | A new code; ends every earlier unused one. 409 `disabled` for a disabled person. |
| `POST /api/v1/admin/people/{id}/disable` and `.../enable` | Disable or enable; answers the person after it. Asking for the state they are in changes nothing. |
| `GET /api/v1/admin/people/{id}/usage?from=&to=` | `PersonUsage`: the period (this month to today when left out, held to `usage.report_max_days`), `total`, `days`, `models`, `failures` by kind and `storage` over time. 400 for a period that cannot be read. |
| `GET /api/v1/admin/install` | `Install`: this month for everyone, the sum of each person's `Use` (total, days, models), `failures`, `cap_micros` (the caps that read, summed), `disk`, `people` (total, enabled, disabled, waiting), `version` (the build Go records) and `sampler` (`every`, `newest`). The install settings that were changed come with P15-03. |
| `POST /api/v1/setup` | The door: `{"code", "password"}`. Sets the password and opens a session in the person's cookie, answering as the sign-in does. 401 `refused` for a code that does not work, 403 `disabled`, 400 `bad_password`. |

Failures by kind are `model_error` and `model_refused` (from the usage records), `task_failed` (tasks that ended failed in the period) and `schedule_start_failed` (schedule runs that could not start). None carries words.

People and Install read every person and write two entries in each person's log, so the screens read them on opening and on **Read again**, never on a timer (the recommended answer in `STATE.md`).

## Adding a person, and setup codes

Decision 0703. A code is 20 characters from 32 that cannot be mistaken (no I, O, 0, 1), in groups of four, 100 random bits; case, spaces and dashes are ignored when it is typed. `data/admin/admin.db` keeps its hash, the person, the issuing account, when made, when it expires (`admin.setup_code_for`, 24h), when used and when ended. The door finds the person by the code, refuses a disabled person and a password the rules refuse without using the code, then uses it in one statement (once, for that person, unexpired), sets the password with `signin.SetPassword` (which ends every session) and signs in. The admin never sees or sets a password; the person's log says a code was made, by whom. A new code is also how a forgotten password is replaced.

## Disabling a person

Decision 0702. The mark is `disabled_at` in the person's own `tenant_meta`, read by `tenant.Store.Disabled`.

- `signin.Disable` writes it and deletes every session in one transaction; `signin.Enable` deletes it. A session is inserted only while the mark is absent, in the same statement, so no sign-in slips through. A disabled person's right password answers 403 `disabled` ("ask the admin"); a wrong one answers 401 as always.
- `unattended.Paused` reads as on while the mark is there: schedules are held (their due times pass and are not made up) and tasks, research and sub-agents pause, as under the person's own switch. Disabling wakes the task runner (`jobs.Runner.PauseAll`); enabling moves due times past now (`schedules.Scheduler.Rearm`), clears the mark and wakes the runner (`ResumeAll`). The view gets these as `adminview.Options` from `internal/app`. The person's own switch is untouched.
- `share.Usable` answers `ErrRevoked`, so a link is 410 `revoked` while they are disabled.
- Their data is kept and stays sealed. Removing a person stays on the command line.

## Operations splits

Decision 0701, changing 0460. The disk left the person's Operations: their report, watch route and home notice hold their own limits only, and the sampler no longer records the disk in their database. The admin account's Install shows it, read live by `figures.ReadDisk` against `operations.watch_disk_percent` as the registry ships it.

## The registry

Decision 0704. Every save of `keystone.json` first adds the people the file lists that the process does not, whose folders are there, so a person added from the command line while the server runs is kept and served. Making a person whose save fails takes out that person by id.

## Settings

| Key | Type | Default | Scope | Meaning |
| --- | --- | --- | --- | --- |
| `admin.setup_code_for` | duration | 24h | install | How long a setup code works. |

## Screens

Provisional on the drafts `design/screens/Admin.html` and `Operations.html`, with Operations' tables, pills and tiles. The admin page (`/admin`) has a top line (who is signed in, theme, sign out) and two tabs:

- **People**: the draft's table with last here, spend of the cap, storage, tasks failed and stuck, schedules, models by health and a state pill (Active, Watch, Waiting for setup, Disabled); **Add a person**, which shows the code once in a card to hand over; **Open**; **Disable** (after a confirmation that says what it does) and **Enable**; **Read again**.
- **A person** (from Open): their tiles, a period with **Read again**, spend by day as inline bars, calls by model, failures by kind and storage over time; **New setup code** (with a warning when they have a password) and Disable or Enable.
- **Install**: spend for everyone against the caps, the disk, the people, the server and its sampler, then spend by day, by model and failures by kind.
- `/setup`: the code, the password twice, and on success the app.
- The person's sign-in says when an account is disabled.

## Checked by

- `go test ./internal/adminview`: `TestTheNumbersAreEachPersonsOwnAndTheInstallsAreTheirSum` (two people with spend, failures, tasks, schedules, files and snapshots: each number equals `operations.Read`, `operations.Watch` and `usagereport.Read`; the database within the entries the read itself writes; the install's equal the sum; no marker word in any reply); `TestStandingIsTheUsageReports`; `TestADisabledPersonIsShutOutAndEnablingRestoresThem` (session ended, sign-in refused with the plain refusal for a wrong password, the switch on, the link revoked, the scheduler holds the schedule, no setup code; enabling restores each; both values in the log); `TestASetupCodeWorksOnce`; the earlier people tests.
- `go test ./internal/setupapi`: a code sets the password once, for its person, not after it expires; a disabled person is refused and the code kept; neither the code nor the password reaches a log.
- `go test ./internal/adminapi`: the people routes, their refusals, and a person's session refused on each.
- `go test ./internal/figures`, `./internal/tenant` (the merge, the mark), `./internal/operations` (no disk in a person's report or snapshots).
- In `verify`: `admin isolation` holds `internal/figures`' imports and walks the new admin routes; `tenant isolation` lists the setup door; `operations` wants no disk in a person's report.

## Not here yet

- Install settings and which were changed (P15-03); views (P15-04); the marker-word drill over every admin route and the server's log (P15-05).
- Passkeys at setup, until `signin.passkeys` is on.
- A trend for the disk in the admin account.
