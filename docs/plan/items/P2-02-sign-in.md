# P2-02. Sign-in and sessions

Status: done
Lane: core
Depends on: P2-01
Owner gate: none
Scope: Password and passkey sign-in; sessions bound to one tenant; sign-out.
Proof: A session for tenant A is refused on tenant B's routes; the tenant isolation check is added to verify.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/signin`, with plain data and functions. It works on one tenant's `Store` through its database. Standard library only.
- A tenant is one person, so there is no table of users. A tenant has at most one password, and any number of passkeys.
- Passwords: a salted PBKDF2-SHA-256 hash from `crypto/pbkdf2`. After a few wrong passwords in a row, password sign-in waits before it takes another.
- Sessions: a row in the tenant's own database. The token is random, names its tenant, and is stored only as a hash. `Check` refuses a token on any store but its own tenant's. `Resolve` turns a token into the one tenant's store, which is what the API (P2-05) calls on every route. Sessions expire, and sign-out ends one or all.
- Passkeys: the server side of WebAuthn with no new module. The browser hands over the public key and the authenticator data as separate fields, so no CBOR reader is needed. Proven against an authenticator written in the tests. The browser's side waits for the sign-in screen, so passkeys sit behind a setting that is off.
- One migration, `20261124T210000_sign_in.sql`, adds the tables.
- `tools/tenant password <tenant-id>` gives a person a password before any sign-in screen exists. It reads the password from standard input.
- Settings in `settings/core.json`: hash iterations, minimum password length, session lifetime, the wait after wrong passwords, and the passkey values.
- A new standing check in `verify`, `tenant isolation`: two tenants in a throwaway data folder, and every way one could reach the other's rows, files or session must be refused.
- Proof: tests show a session for tenant A is refused on tenant B's store and resolves only to A's, including when a session row or a passkey row is copied across. The same holds in `verify`.
- Docs: `docs/architecture/signin.md`, a row in the overview, decision 0007 on how a token finds its tenant, decision 0012 on passkeys without a CBOR reader, `tools.md`, and the runbook for adding a person.

### Result

Done on November 24, 2026.

- `internal/signin` holds passwords, sessions and passkeys, each in the tenant's own database. Standard library only, no new module.
- Passwords: `SetPassword`, `HasPassword`, `SignIn`. PBKDF2-SHA-256 with a salt, 600,000 rounds as shipped. After five wrong passwords in a row, password sign-in waits a minute.
- Sessions: `Check`, `Resolve`, `TenantOf`, `SignOut`, `SignOutAll`. A token names its tenant and is stored only as its SHA-256. `Resolve` is what the API (P2-05) calls on every route.
- Passkeys: `BeginAddPasskey`, `AddPasskey`, `BeginPasskeySignIn`, `PasskeySignIn`, `Passkeys`, `RemovePasskey`. Passkeys were not split into another item. They are behind `signin.passkeys`, which is off: the code is proven against an authenticator written in the tests, and no real browser has used it. The sign-in screen (P4-01) proves it in a browser and switches it on.
- Proof: `go test ./internal/signin` shows a session for tenant A refused on tenant B's store, refused when re-addressed to B, and refused when its row is copied into B's database, and that A's token resolves only to A. The same holds for a passkey. `verify` has the new standing check `tenant isolation` and is 9 of 9. Taking the tenant test out of `Check` makes both the tests and the new check fail.
- There are no routes yet, so "refused on tenant B's routes" is proven on the function every route will call. The route walk is P2-05's proof.
- `go run ./tools/tenant password <tenant-id>` sets a password from standard input.
- Nine settings under `signin.` in `settings/core.json`.
- Decisions 0007 (a session token names its tenant) and 0012 (passkeys without a CBOR reader).
- One question for the owner, in the journal entry: how the sign-in screen asks who is signing in.
- Doc: `docs/architecture/signin.md`. Journal: `docs/journal/2026-11-24-core-005.md`.

