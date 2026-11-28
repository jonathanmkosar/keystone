# P15-01. The admin account

Status: provisional (core): A1 was answered on November 27, 2026 as a separate account, so the first pass is reworked
Lane: core
Depends on: P2-02, P2-04, P2-05
Owner gate: None: A1 and A2 are answered.
Built without the gate: Everything.
Scope: Admin accounts in their own database, `data/admin/admin.db`: accounts, passwords and passkeys, sessions with `ksa_` tokens, and the admin audit log. `tools/admin add` and `password`, `/admin/sign-in`, the session and idle limits, and a door that tells a person's token from an admin's before anything is read. Remove the first pass's mark, `tools/tenant admin` and the step-up. Keep `internal/adminview` and the two audit logs, and show a person the admin entries of their own log in their Settings. A decision record replaces 0640 and 0643.
Proof: A person's token is refused on every admin route and an admin's on every person's route. An admin account has no chat, file or model route. Every admin call is in both audit logs, and the person sees it.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "The admin account" and "What an admin never sees", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `core/p15-01-admin-mark`, built on the assumed answers A1 and A2.

- **The mark.** `tenant.Tenant` gains `admin`. `Install.SetAdmin` is the only writer, and `go run ./tools/tenant admin <id> on|off` its only caller; it refuses to take the last admin's mark without `--yes`. The mark is read from `keystone.json` on disk at each admin call (`Install.IsAdmin`), so a change from the command line counts at once in a running server, and every save keeps the marks that are on disk. A backup carries no mark and a restore grants none.
- **Step-up.** A migration adds `admin_until` to `signin_sessions`. `signin.StepUp` checks the password with the sign-in's own count of wrong tries; `signin.BeginPasskeyStepUp` and `signin.PasskeyStepUp` do the same with a passkey. `admin_until` is now plus `admin.step_up_for` (15m, install), never past the session's end. `signin.EndStepUp` ends it.
- **The door.** A route under `/api/v1/admin/` is marked `Admin`, and `api.Check` refuses one that is not, or one outside it that is. In the shell, a call to an admin path made with a marked person's session is recorded with the actor `admin`; a person without the mark is refused 403 `not_admin` on every admin path, and a session without a fresh step-up 401 `step_up`, except on the routes marked `StepUp` (the step-up itself), which `tools/verify` lists as it lists doors.
- **Routes (contract first).** `POST /api/v1/admin/step-up`, `POST /api/v1/admin/step-up/passkey`, `DELETE /api/v1/admin/step-up`, and `GET /api/v1/admin/people` with each person's id, name, mark, when added and last sign-in (P15-02 adds the numbers). `GET /api/v1/session` gains `admin` and `admin_until`. In a new package, `internal/adminapi`, which is given an `adminview.View` and never the install.
- **`internal/adminview`.** The only code the admin routes use to read another tenant. Each read of another person is written to that person's audit log first, with the actor `admin:<the admin's id>`, and nothing is read if it cannot be.
- **The check.** A new standing check, `admin isolation`: `internal/adminview` and `internal/adminapi` import none of the packages that hold a person's content, directly or through another; `adminapi` does not import `internal/tenant`; `SetAdmin` is called from `tools/tenant` alone; and a walk of every admin route with a person without the mark, an admin without a step-up, and an expired step-up, each refused and recorded, then the people route with a fresh step-up written in both logs.
- **Proof.** Tests in `tenant`, `signin`, `api`, `adminview`, `adminapi` and `tools/tenant`; `verify` green with 46 checks.

## Result

Built as planned, November 27, 2026; see `docs/architecture/admin.md`, decisions 0640 to 0646 and the journal `docs/journal/2026-11-27-core-015.md`. One change from the plan: a second migration lets a passkey prompt have the purpose `step-up` (the challenges table's CHECK named only `add` and `sign-in`). Provisional on the owner's answers A1 and A2: if either changes, the mark, the step-up and what `adminview` returns are what is revisited; the door, the audit entries and the check keep their shape.
