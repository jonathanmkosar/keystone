# P2-04. Audit log and per-tenant settings

Status: done
Lane: core
Depends on: P2-01
Owner gate: none
Scope: Append-only audit log per tenant; tenant overrides over registry defaults, limited to settings marked overridable.
Proof: Every API call lands in the audit log; an override test passes; a fixed rule cannot be overridden.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/audit`, that works on a tenant's store handle. It appends an entry and lists entries, and does nothing else. One migration adds the `audit_log` table with triggers that refuse a change, a delete or a replace, so the log is append-only in the database itself and not only in this package.
- An entry says who and when, the kind of call (API, model or tool), the request id, what was sent and what came back, the tokens (the seam's `Usage`), the cost, how long it took and the outcome.
- Keys are kept out three ways. The API wrapper records no header, no query string and no body. A caller that holds a key names it, and that exact value is removed. Everything written is searched for the shapes of keys, and a match is removed. A test reads the rows back and looks for each planted key.
- The API does not exist yet (P2-05). This item builds the `net/http` wrapper P2-05 puts around its routes. It writes a `started` entry before the handler runs and refuses the call when that fails, so a call cannot be served without an entry. It writes a second entry with the status when the handler ends. Proven on a test server.
- A new package, `internal/tenantsettings`, with one table of stored values. A person may set a setting whose scope is `tenant`, the admin sets a `tenant-admin` one, and an `install` one cannot be set for a tenant. Values are checked with `settings.CheckValue`. `Get` returns the value in force for a tenant.
- The four fixed rules get a concrete form: a list in the `settings` package, each with a name under `rules.`, a part of the key space no setting may use. The registry refuses a setting there, `tenantsettings` refuses to store one, and so does the table.
- A new standing check, `fixed rules`, in `tools/verify/fixedrules.go`: the list matches PLAN.md word for word, no setting sits under `rules.`, no override path stores one, and the packages that enforce a rule cannot read a setting at all. Later items add their package to the check as each rule gets code.
- Proof: tests for each of the above, and `verify` green with the new check.
- Docs: `docs/architecture/audit.md`, `docs/architecture/tenant-settings.md`, the fixed rules in `settings.md`, rows in the overview and in `tools.md`, and decisions 0009 and 0014.


### Result

Done on November 24, 2026, as far as it can be before the API exists.

- `internal/audit`: an append-only `audit_log` table in each tenant's database, kept so by triggers, with keys removed three ways, and `Wrap`, which records a call before it runs and again when it ends (decision 0009).
- `internal/tenantsettings`: a tenant's values over the registry's defaults, limited by each setting's scope. The four fixed rules are in `settings/fixed.go` and cannot be stored (decision 0014).
- `verify` has the `fixed rules` check. At the merge it caught P2-03's tenant package reading a setting, and that was fixed in P2-03's code.
- The proof: an override test passes, and a fixed rule cannot be overridden by a person, the admin, or a row written straight into the database. "Every API call lands in the audit log" is proven for any handler put inside `Wrap`. There are no routes yet, so P2-05 wraps its routes and adds the walk that proves it for each one.
- At the merge, `internal/audit` joined the offline check's list of folders that may import a network package: it wraps the server's handlers and opens no connection.
- The lane's session stopped after its code and docs were committed. The journal entry was written by the integrator.

Journal: `docs/journal/2026-11-24-core-007.md`.
