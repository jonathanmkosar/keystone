# P15-03. Admin settings from the browser

Status: provisional, on the owner gate (the defaults for a person's own settings are assumed, decision 0712) and on the mockups for the screens (decision 0715)
Lane: core + interface
Depends on: P15-01
Owner gate: The defaults for a person's own settings are assumed.
Built without the gate: Everything, with those defaults provisional.
Scope: Install settings stored in `keystone.json`, the registry's `restart` field, the routes and screens for a person's `tenant-admin` settings and the install's settings, and install-wide defaults for each person's own settings. The router's policy (P14-05) is set here.
Proof: An admin changes a person's `tenant-admin` setting and the person's next turn uses it; the admin cannot change a person's own setting; a new person starts with the defaults. Each change is in both audit logs with its old and new value.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Settings for people", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `core/p15-03-admin-settings`, cut from `reboot`. Built beside P15-02 (people and monitoring) and P14-05 (the router's `tenant-admin` policy), so new code goes in new files and shared files only gain lines.

- **Install values in `keystone.json`.** Two objects: `settings` (key to value as text) for `install` settings, and `person_defaults` for the install-wide defaults of each person's own (`tenant`) settings. `internal/tenant` gains a new file that reads and writes them under its lock, re-reading the file first and keeping every field and every key it does not know. It still reads no setting.
- **The registry.** An optional `restart` field (bool). Install values reach the code through one overlay in `settings`: `settings.UseInstalled` is given the stored values, `Registry()` gives them as each setting's `Default`, and `settings.DefaultFor` answers the value in force for code that kept a registry from before a change. `tenantsettings` uses `DefaultFor` where it fell back to the default, so a person with no value of their own follows the install's default at once, and a new person starts with it. A person's own value always wins.
- **`internal/installsettings`** (new): load at start (every stored value), change one value (validated by type and options; `tenant-admin` and fixed rules refused; `data.dir` refused because it names the folder the file is in), and list for the screens (shipped default, stored value, value in force, restart, waiting for a restart). A change to a `restart` setting is stored and takes effect at the next start; any other at once.
- **`restart` marks.** Every `install` setting the server reads once at start is marked, starting with each one `app.ReadConfig` reads; a test in `internal/app` fails when one is read at start and not marked.
- **Admin routes (contract first)**, in a new file of `internal/adminapi`, through new functions of `internal/adminview`:
  - `GET /api/v1/admin/people/{id}/settings`: the person's `tenant-admin` settings, each with its value, default, whether it is set for them, and when.
  - `PUT /api/v1/admin/people/{id}/settings` with `{"key", "value"}` or `{"key", "reset": true}`: through `tenantsettings.Set` or `Clear` with the role `admin`, so a person's own setting is refused (403 `not_allowed`), as is a fixed rule.
  - `GET /api/v1/admin/install/settings`: every `install` setting and every person's default, with shipped, stored, in force, `restart` and `pending`.
  - `PUT /api/v1/admin/install/settings` with the same body: the key's scope says whether it is an install value or a person's default.
  - **Both logs.** A person's change is in their own log before and after (`adminview.Touch` with the key, the new value, and then the old), and in the admin log as its own pair with the person, key, old and new beside the door's pair. A refused change reads and writes nothing of the person's. An install change is in the admin log with its old and new value; it is no person's.
- **Screens**, provisional on the Admin draft's Settings tab: Admin â†’ Settings (install settings and person defaults, searchable by section, each marked "after a restart" where it is) and A person â†’ Settings (their `tenant-admin` settings, the router's policy among them, drawn from the registry by scope and naming no key). New files beside `AdminApp.tsx`, which gains only the paths and links.
- **Proof.** Go tests: the file keeps unknown fields and keys; the overlay, `DefaultFor` and a new person's defaults; the routes: an admin sets the test tenant's `router.ladder` and `router.decision_model` and the person's next turn on the real handler is routed by Auto; a person's own setting and a fixed rule refused with nothing written in the person's log; each change in both logs with old and new; install values read at start and on each change; a `restart` change waits. `verify` green.
- **Provisional:** the person defaults' semantics (owner gate), and the screens on the mockup.

## Result

Built as planned on November 28, 2026; see `docs/architecture/admin-settings.md`, decisions 0710 to 0715 and the journal `docs/journal/2026-11-28-core-019.md`. Install values and person defaults are kept in `keystone.json` (`settings`, `person_defaults`) and reach the code through one overlay on the registry, which `tenantsettings` falls back to. The registry's `restart` field marks the 172 install settings read once at start, held by a test. `GET` and `PUT /api/v1/admin/people/{id}/settings` and `/api/v1/admin/install/settings` go through `adminview`; each change of a person's setting is in their log and the admin log with its old and new value, an install change in the admin log. The proof runs on the real handler: the admin sets the test tenant's `router.ladder` and `router.decision_model` and the person's next turn is routed by Auto; a person's own setting and a fixed rule are refused with nothing written; a new person, and anyone who has not chosen, starts with a person default. The screens (Admin → Settings, People, a person's Settings) are drawn from the registry by scope and wait for the integrator's `web/dist` rebuild to be seen.
