# P2-01. Tenant folders and databases

Status: done
Lane: core
Depends on: P0-02
Owner gate: none
Scope: A tenant is one person. Tenant registry in keystone.json; one folder and one SQLite database per tenant; numbered forward-only migrations; a store handle that can only address its own tenant.
Proof: Two tenants are created; a handle for one cannot read the other's rows or files; migrations run from an empty file.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan:

- `internal/tenant` holds the install and the store handle. The install reads and writes the tenant registry in `keystone.json`, keeping any fields it does not know. Creating a tenant makes `tenants/<id>/` with `keystone.db`, `files/` and `artifacts/`.
- A tenant id is sixteen random hex characters. A handle is opened by id, and only for an id in the registry, so a path can never be passed in as an id.
- A store handle holds its own database and its own folders, opened as `os.Root`, which refuses any path that leaves the folder. It has no way to name another tenant.
- Each database records its tenant id. Opening a database whose id is not the folder's id is refused, so a copied file cannot be read as someone else's.
- Migrations are SQL files named by date and time, as PARALLEL.md asks, and applied in name order, forward only. Each runs in its own transaction and is recorded by name. This differs from the item's word "numbered": PARALLEL.md is the later text and exists so two lanes can add migrations at once.
- The driver is `modernc.org/sqlite` at the version Waymarks uses, with the same connection settings: write-ahead log, a wait when busy, foreign keys on.
- `tools/tenant` adds and lists tenants. `rebuild` gains a step that opens the data folder and brings every tenant's database up to date.
- The item's scope says numbered migrations. PARALLEL.md, the later text, says migrations are named by date and time so two lanes can add them at once, and decision 0003 follows it. Files are named by date and time and applied in name order.
- Proof: tests create two tenants and show that one handle cannot read the other's rows or files, that migrations run from an empty file and only once, and that a database from another tenant is refused.

