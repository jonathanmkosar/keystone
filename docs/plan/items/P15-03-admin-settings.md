# P15-03. Admin settings from the browser

Status: todo
Lane: core + interface
Depends on: P15-01
Owner gate: The owner's answer A1.
Built without the gate: Everything, provisional on the answer.
Scope: Install settings stored in `keystone.json`, the registry's `restart` field, and the routes and screens for a person's `tenant-admin` settings and the install's settings. The router's policy (P14-05) is set here.
Proof: An admin changes a person's `tenant-admin` setting and the person's next turn uses it. The person cannot change it. Each change is in both audit logs with its old and new value.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Settings from the browser", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
