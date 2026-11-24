# P2-01. Tenant folders and databases

Status: todo
Lane: core
Depends on: P0-02
Owner gate: none
Scope: A tenant is one person. Tenant registry in keystone.json; one folder and one SQLite database per tenant; numbered forward-only migrations; a store handle that can only address its own tenant.
Proof: Two tenants are created; a handle for one cannot read the other's rows or files; migrations run from an empty file.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

