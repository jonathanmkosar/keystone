# P15-03. Admin settings from the browser

Status: todo
Lane: core + interface
Depends on: P15-01
Owner gate: The defaults for a person's own settings are assumed.
Built without the gate: Everything, with those defaults provisional.
Scope: Install settings stored in `keystone.json`, the registry's `restart` field, the routes and screens for a person's `tenant-admin` settings and the install's settings, and install-wide defaults for each person's own settings. The router's policy (P14-05) is set here.
Proof: An admin changes a person's `tenant-admin` setting and the person's next turn uses it; the admin cannot change a person's own setting; a new person starts with the defaults. Each change is in both audit logs with its old and new value.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Settings for people", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
