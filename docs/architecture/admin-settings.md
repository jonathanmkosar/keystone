# Admin settings from the browser

Folders: `internal/installsettings`, with `settings/installed.go` (the overlay), `internal/tenant/installvalues.go` (the file), `internal/adminview/settings.go` and `internal/adminapi/settings.go` (the routes), `cmd/keystone/installsettings.go`. Screens: `web/app/src/AdminSettings.tsx`, `adminSettingsCalls.ts` and `admin-settings.css`.

P15-03. The design is `docs/plan/SPEC-router-and-admin.md`, Part 2, "Settings for people", with the admin account of [admin.md](admin.md) and the registry of [settings.md](settings.md). Decisions 0710 to 0715.

## Who sets what

| Scope | One value for | Set by | Where it is kept | From the browser |
| --- | --- | --- | --- | --- |
| `install` | The whole install | The admin | `keystone.json`, `settings` | Admin → Settings → The whole install |
| `tenant-admin` | Each person | The admin | The person's database (`tenant_settings`, `set_by` `admin`) | Admin → People → a person → Settings |
| `tenant` | Each person | That person | The person's database (`set_by` `person`) | The person's Settings → General |
| `tenant`, as a default | Everyone who has not chosen | The admin | `keystone.json`, `person_defaults` | Admin → Settings → Each person starts with |

The fixed rules are not settings and are refused everywhere, for every role. `data.dir` cannot be kept in `keystone.json`, which is in it (decision 0710).

## The install's values

`keystone.json` gains two objects, of key to value as text:

```json
{
  "settings": { "usage.report_max_days": "90", "server.address": "0.0.0.0:7900" },
  "person_defaults": { "appearance.theme": "dark" },
  "tenants": [ ... ],
  "version": 1
}
```

`internal/tenant` reads them from disk at each read and, at each write, reads the file again, changes only its own object and keeps every field and key it does not know (`Install.InstallValues`, `ChangeInstallValues`). It reads no setting.

`internal/installsettings` says what they mean:

- `Load(in)`: at start, before any setting is read, every stored value goes to the registry's overlay (`settings.UseInstalled`). `keystone serve` calls it in `openAPIWith`, and takes `server.address` from it when `--address` is not given.
- `List`, `Get`: each setting the install may set, with its shipped default, the stored value, what the install has chosen (`Set`), what Keystone runs with now (`InForce`) and whether a restart is waited for (`Pending`).
- `Set`, `Clear`, `Check`: one change, checked by type and options, written to the file, then handed to the overlay, except a `restart` setting, which keeps its start value until the next `Load`.

## The overlay

`settings.UseInstalled(values)` holds the install's values for the process (decision 0711):

- `settings.Registry()` gives each as its setting's `Default`; `settings.Shipped(st)` the lane file's default.
- `settings.DefaultFor(st)` gives the default in force now, for a registry read before a change.
- `tenantsettings.Get` and `All` fall back to `DefaultFor`, so a person with no value of their own follows the install's default at once, and a new person starts with it (decision 0712). A person's own value always wins, and the admin cannot set or clear it.
- Only `install` and `tenant` settings take an install value; a value that does not fit is passed over.

## Restart

The registry's optional `restart` field marks an `install` setting the server reads once, at start (decision 0713). 172 are marked: every install setting `app.ReadConfig` reads, those `keystone serve` reads before it serves, and the model adapters'. A change to one is kept in `keystone.json` and waits; the screens say "After a restart" on each, and "Waits for a restart" with the value Keystone runs with until then once one is changed. `TestEveryInstallSettingReadAtStartIsMarkedRestart` (`internal/app/restart_test.go`) fails when a setting read at start is not marked.

## Routes

All behind the admin door ([admin.md](admin.md)), all through `internal/adminview`.

| Route | What it does |
| --- | --- |
| `GET /api/v1/admin/people/{id}/settings` | The person's `tenant-admin` settings, each with its title, summary, type, options, description, default, value, whether it is set for them, and when. Never their own settings. Their log records the read, `{"by", "read": ["settings"]}`. 404 `not_found` for an id that is no person. |
| `PUT /api/v1/admin/people/{id}/settings` | `{"key", "value"}` sets one; `{"key", "reset": true}` takes it back to its default. Through `tenantsettings` with the role `admin`. 403 `not_allowed` for a person's own setting, an install setting or a fixed rule; 404 `unknown_setting`; 400 `bad_value`. Answers the setting after and the change. |
| `GET /api/v1/admin/install/settings` | Every `install` setting but `data.dir`, and every `tenant` setting as a person default, with `default` (shipped), `value` (chosen), `overridden`, `restart`, and for an install setting `in_force` and `pending`. |
| `PUT /api/v1/admin/install/settings` | The same body. The key's scope says whether it is an install value or a person default. 403 `not_allowed` for a `tenant-admin` setting, a fixed rule or `data.dir`. |

The shapes are in `docs/api/openapi.json`: `adminapi.ChangeBody`, `PersonSettingsReply`, `InstallSettingsReply`, `ChangedReply`, `adminview.Setting` and `adminview.Change`.

## Both logs

Decision 0714. A person's change is checked before anything of theirs is read; a refusal is in the admin log through the door's pair alone. Then:

1. the admin log: `started`, `{"by", "person", "key", "new"}`;
2. the person's log: `started`, the same;
3. the old value read, the change made;
4. the person's log, then the admin log: `ok` or `error`, with `"old"` too, even when it is empty.

An install change is in the admin log as its own pair with `old`, `new` and `restart`, and in no person's log. Every entry carries the admin call's request id and route, as the door's pair does. The person sees the admin's changes, with the key, in Settings → Admin access.

## Screens

Provisional on the mockups: the Admin draft (`design/screens/Admin.html`) has a Settings tab and no drawing of it (decision 0715).

- `/admin/settings`: two parts, "The whole install" and "Each person starts with", each with a search, a section, and "Changed"; each row as on a person's General screen (title, short line, value; opened, the explanation, the box, the default with Reset, the key), with "After a restart" or "Waits for a restart".
- `/admin/people`: the people, each with a link to their settings. A stand-in until P15-02's People screen.
- `/admin/people/{id}/settings`: the person's `tenant-admin` settings, the router's policy among them.

Rows are drawn from the registry by scope and name no setting, so a setting another lane adds appears with no change here. `AdminApp.tsx` draws these screens for their paths once the admin is signed in, and its overview links to them.

## Checked by

- `go test ./settings`: install values as defaults, only for `install` and `tenant`; a value that does not fit passed over; `DefaultFor` on a registry read before a change and after one is taken away; `restart` only on an install setting.
- `go test ./internal/tenant`: the two objects kept with a field and a key another program wrote; a failed change writes nothing; a reopened install keeps them.
- `go test ./internal/installsettings`: a change in force at once and read again at start; a restart setting waits, through later changes, until `Load`; what cannot be set is refused and never written; a person default reaches a new person and one who has not chosen, and a person's own choice stays theirs.
- `go test ./internal/adminview`: the read in the person's log; a change in both logs with old and new; a reset; refusals (a person's own setting, an install setting, a fixed rule, an unknown key, a bad value, no such person, no such admin) write nothing in either; an install change in the admin log alone.
- `go test ./internal/app`: on the real handler, an admin sets the test tenant's `router.ladder` and `router.decision_model`, and the person's next turn is routed by Auto on that ladder; the admin cannot set a person's own setting or a fixed rule; a person's token is refused; install values, a restart setting that waits, a person default the person sees, refusals and reset; and the restart marks.
- `verify`, the `admin isolation` check: the new routes are admin routes, refused to a person's token, and in the admin log.

## Not here yet

- Install-wide defaults for `tenant-admin` settings (the router's ladder for everyone, say): the spec gives defaults for a person's own settings only.
- The admin's view of how many people follow a default.
- The tenant list is still written from memory: until P15-02's merge, a person added from the command line while the server runs is dropped by a settings change too.
