# P11-01. Settings screens

Status: doing (interface)
Lane: interface
Depends on: P2-04, P4-01
Owner gate: Approval of the settings mockups (P1-07).
Built without the gate: Built from the draft mockups, marked provisional.
Scope: Profile, appearance, models and keys, connectors, approvals, spend.
Proof: Every overridable setting in the registry has a control; a doc check proves it.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan, November 25, 2026

Keystone runs in a browser with a working chat, and a person still cannot make it theirs from there: a key and a model are set from the command line, and the profile and a person's own settings have no way in. This pass gives those four things routes and screens.

The contract, written first:

- `GET /api/v1/settings` lists every setting with its value, default, type, options, scope and description, and `changeable` for the signed-in person. The four fixed rules are listed apart and have no value. `PUT` and `DELETE /api/v1/settings/{key}` store and remove the person's own value through `internal/tenantsettings`. A write to a fixed rule is refused.
- `GET /api/v1/secrets` lists names. `PUT` and `DELETE /api/v1/secrets/{name}` store and remove one. No route returns a value.
- `GET` and `POST /api/v1/models`, `PUT` and `DELETE /api/v1/models/{name}`: the person's registry. Each row says whether its key is stored, by name.
- `GET` and `PUT /api/v1/profile`.

The routes are a new package, `internal/settingsapi`, added to the list in `internal/app/app.go`. The packages that guard a fixed rule gain no import: the new package reads the settings they need and passes the values in.

The screens, under `/settings`, reached from the person's name in the sidebar: Profile, Appearance, Models and keys, and General. General draws one control for every setting the route marks changeable, grouped by lane, from the route's data and with no code for any one setting. Connectors, approvals and spend have no code behind them, so each gets a screen that says which item brings it.

There is no admin role in the code. Every route acts for the signed-in person on their own tenant. What rests on the open questions in `STATE.md` (who may change a model registry, the profile going to every model) is built to the "assumed meanwhile" answer and marked provisional.

Proof: a Go test of the settings route, on the real registry, shows that every setting whose scope lets a person change it is returned as changeable, is of a type the General screen has a control for, and can be stored and reset; that no other setting is; and that no fixed rule is a setting or can be written. The doc says how the screen is generated from that route. The secret routes join the `no keys in output` check. `verify`, `npm run build` and `rebuild` are green, and the screens are used in a browser as the test tenant.

