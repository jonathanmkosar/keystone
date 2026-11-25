# P2-05. API shell and streaming

Status: doing (core)
Lane: core
Depends on: P2-02, P2-04
Owner gate: none
Scope: HTTP API skeleton, server-sent events transport, OpenAPI document, request ids, the command-line client.
Proof: A route walk proves every route needs a session and resolves the tenant from it.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What will change:

- A new package, `internal/api`: the mechanism. A route is a row of data (method, path, summary, the shapes it takes and returns, its handler). One function turns a list of routes into the handler for `/api/`. Every route but a short, named list of door routes (the ones a person needs before a session exists: sign-in) goes inside one `audit.Wrap`, whose `who` reads the session cookie and calls `signin.Resolve`. A handler is handed the one store its session resolved to, and has no other way to a tenant.
- A new package, `internal/app`: the one list of every route Keystone ships. The binary and `verify` both read it, so the walk sees what is served.
- Request ids on every response, a JSON error shape, a limit on body size, a cross-origin check, and the server's own log, with keys removed by `audit.Scrub`.
- Server-sent events: `api.OpenStream`, events with an id, a name and JSON data, a heartbeat, and `Last-Event-ID`. One test stream route, so a client's reader can be built before turns exist.
- The OpenAPI document, built from the route list, served by a route, printed by `keystone openapi`, and committed at `docs/api/openapi.json`. A test fails when the committed file is not what the routes say.
- The shell's own routes: who is on the install, sign in, who am I, sign out, read my audit log, the OpenAPI document, the test stream.
- The session token travels in a cookie page scripts cannot read (`HttpOnly`, `SameSite=Strict`), and in no response body.
- `keystone serve` opens the data folder, reads `secrets.keeper` and calls `Install.NewInstallKeeper`, and serves the API beside the page.
- The command-line client, in `cmd/keystone`, as a client of the same API: `people`, `signin`, `signout`, `whoami`, `api <METHOD> <path>` (which prints a stream as it arrives) and `openapi`.
- New settings in `settings/core.json` for the values someone might change.

How it will be proven:

- `go test ./internal/api ./internal/app ./cmd/keystone`.
- A route walk in the `tenant isolation` check: every route that is not a listed door route is refused with no session, with a session re-addressed to another tenant and with a signed-out session, and a call with tenant one's session is written in tenant one's audit log and not in tenant two's. A door route that is not on the check's own list fails the check.
- A new check, `calls recorded`: every route called with a session leaves a `started` entry and an end entry, and a sign-in leaves an entry in the log of the tenant it was for.
- The `no keys in output` check gets the API as a surface: a stored secret and the session token are looked for in every response, the server's log and the data folder.

Open question, built to the assumption in `STATE.md`: the sign-in route is told the tenant by its id, and a door route lists the people on the install, behind the setting `signin.list_people`. Both are provisional until the owner answers.

Not in this item: passkey routes (off until P4-01 proves them in a browser), routes that change a password or a setting (P11-01), hiding a typed password at the console.

