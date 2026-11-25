# Settings screens

Folders: `internal/settingsapi`, and the screens in `web/app/src/`

A person makes Keystone theirs from the browser: they store a provider key, register a model, write their profile and change a setting. This part is the routes that do those four things and the screens under `/settings` that call them. It was built in P11-01 and is provisional: see "Provisional" at the end.

Before it, a key and a model were set from the command line only (`tools/tenant secret`, `tools/tenant model`), and the profile and a person's own settings had no way in at all. The commands still work, and the [model runbook](../runbooks/adding-a-model.md) still describes them.

## The routes

`internal/settingsapi` exports `Routes()`, and `internal/app/app.go` adds them to the one list ([api.md](api.md)). Each acts for the signed-in person on their own tenant, with the store the shell hands it. There is no admin role in the API yet, so every call is made as the person.

| Route | What it does |
| --- | --- |
| `GET /api/v1/settings` | Every setting as it stands for the person, and the four fixed rules, apart. |
| `PUT /api/v1/settings/{key}` | Store the person's own value: `{"value": "light"}`. |
| `DELETE /api/v1/settings/{key}` | Remove the person's own value, so the default is back. |
| `GET /api/v1/secrets` | The names of the stored secrets, and the models that use each. |
| `PUT /api/v1/secrets/{name}` | Store a secret: `{"value": "..."}`. Stores over an earlier one. |
| `DELETE /api/v1/secrets/{name}` | Remove one. |
| `GET /api/v1/models` | The person's model registry, and the choices a form offers. |
| `POST /api/v1/models` | Add a model. The body is an entry, as in [models.md](models.md). |
| `PUT /api/v1/models/{name}` | Replace one entry. |
| `DELETE /api/v1/models/{name}` | Remove one. |
| `GET /api/v1/profile` | The profile's text, when it was set, and the most characters it may hold. |
| `PUT /api/v1/profile` | Replace the profile: `{"text": "..."}`. An empty text clears it. |

`docs/api/openapi.json` has every shape.

The packages underneath guard fixed rules and read no setting: the tenant store, the audit log, the model registry and the profile. This package reads the two values they need, `models.max_per_tenant` and `knowledge.profile_max_chars`, and passes them in. The `fixed rules` check would fail if one of those packages read a setting itself.

### Settings

`GET /api/v1/settings` answers with `settings` and `rules`.

A setting has the registry's own fields, `key`, `lane`, `type`, `default`, `options`, `scope` and `description`, and three for this person:

| Field | Meaning |
| --- | --- |
| `value` | The value in force: the person's own, or the default. Text, read as `type` says. |
| `overridden` | The value is the person's own. |
| `changeable` | The signed-in person may change it. This is `tenantsettings.MayChange` for the person: true when the scope is `tenant`, and false for `tenant-admin` and `install`. |

Every setting in the registry is listed, the ones a person may not change too, so a person can see what the install runs with. An `install` setting is listed with its default: there is nowhere yet to store an admin's change ([settings.md](settings.md)).

A change goes through `internal/tenantsettings`, which checks the scope and the value ([tenant-settings.md](tenant-settings.md)).

**The fixed rules are not settings.** `rules` lists the four with their words and nothing else: no value, no default, no `changeable`. A rule's key is never in `settings`. A `PUT` or a `DELETE` on a key under `rules.` is refused with 403 and the code `fixed_rule`, before the registry is read, and nothing is stored.

| Code | Status | Meaning |
| --- | --- | --- |
| `fixed_rule` | 403 | The key names a fixed rule. |
| `not_allowed` | 403 | The setting is the admin's to change. |
| `not_found` | 404 | No such setting. |
| `bad_value` | 400 | The value does not read as the setting's type, or is not one of its options. The message says what the setting takes and does not repeat what was sent. |

### Secrets

A secret is a provider key or a connector token in the person's vault ([secrets.md](secrets.md)).

**A value goes in and never comes out.** No route returns one. `GET /api/v1/secrets` returns names. The answer to a `PUT` is the name and the models that use it. A refusal never repeats the name or the value it was sent, in case a key was pasted where the name goes. The audit log holds the path of a call and never its body ([audit.md](audit.md)), so storing a key leaves the name in the log and not the value.

A name is 1 to 100 lower-case letters, digits, dots, dashes and underscores, such as `provider.example`. A value is text of 1 to 65,536 bytes.

`used_by` lists the models whose `key` is this name, so the screen can warn before a key in use is removed. Removing it is allowed all the same. The models that named it then show their key as missing.

| Code | Status | Meaning |
| --- | --- | --- |
| `bad_name` | 400 | Not a secret's name. |
| `bad_value` | 400 | Empty, too long, or not text. |
| `not_found` | 404 | Delete only: no secret of that name. |

### Models

`GET /api/v1/models` answers with `models`, and with `adapters`, `classes`, `capabilities` and `max`, which are the choices and the limit a form needs.

Each row of `models` holds:

| Field | Meaning |
| --- | --- |
| `entry` | The entry as the registry keeps it. Its `key` is the name of a secret and never the secret. |
| `key_present` | A secret of that name is stored. True for an entry that names no key. |
| `may_read_sensitive` | `Model.MayReadSensitive()`: the class is `local` or `trusted`. |
| `updated` | When the entry was last stored. |
| `problem` | Why a stored entry is refused today. Such a row has only its name, and can be replaced or removed. |

`POST` and `PUT` take an entry and run the registry's own `Check`, so everything [models.md](models.md) lists as refused is refused here, with the registry's reason as the message. That reason names the field and never repeats an endpoint, a key name or a certificate. `PUT` keeps the name in the address: a model is renamed by removing it and adding it again.

| Code | Status | Meaning |
| --- | --- | --- |
| `bad_model` | 400 | The entry is refused. The message says why. |
| `exists` | 409 | Add only: the name is taken. |
| `full` | 409 | Add only: the registry holds `models.max_per_tenant` models. |
| `not_found` | 404 | Replace and remove: no model of that name. |

The test tenant's model is in no registry (decision [0144](decisions/0144-the-test-tenants-model-is-in-no-registry.md)), so its list is empty until it adds one.

### Profile

The profile is one text ([knowledge-profile-and-map.md](knowledge-profile-and-map.md), decision [0180](decisions/0180-the-profile-is-one-text.md)). `PUT` trims it, makes its line ends plain and refuses one longer than `max_chars` with the code `too_long`. A text that is not plain text is refused with `bad_text`.

## Checked by

- `go test ./internal/settingsapi`.
  - `TestEveryOverridableSettingHasAControl` is the item's proof. See "The proof" below.
  - A person's override is theirs and not another person's. A value that does not fit, an unknown key and a fixed rule are refused, and a refusal does not repeat the value.
  - A secret is stored for the person and not for another, is listed by name, and its value is in no answer of any route.
  - A model is added, listed, replaced and removed. It says whether its key is stored, before and after the key arrives. A second model of the same name, one over the limit, a key pasted as a key name and a `local` class on an outside address are refused.
  - The profile is read, written, cleared, held to its limit, and is the person's own.
- The `no keys in output` check has a walk of its own for these routes, `tools/verify/settingsroutes.go`. It stores an invented value through `PUT /api/v1/secrets/{name}`, then calls the routes that could hand it back, and sends the value where it does not belong: as a secret's name, a model's key name and endpoint, a setting's value. The value must be in no answer, not in the server's log, and in no file of the data folder.
- The route walks in `tenant isolation`, `calls recorded` and `no keys in output` reach these routes with every other, because they read `app.Routes` ([tools.md](tools.md)).
