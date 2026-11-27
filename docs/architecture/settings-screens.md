# Settings screens

Folders: `internal/settingsapi`, and the screens in `web/app/src/`

A person makes Keystone theirs from the browser: they store a provider key, register a model, write their profile and change a setting. This part is the routes that do those four things and the screens under `/settings` that call them. It was built in P11-01 and is provisional: see "Provisional" at the end. Since P11-04 the General screen shows each setting as a name, one short line and its value, with the long explanation one step away: see "How General is drawn".

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

A setting has the registry's own fields, `key`, `title`, `summary`, `lane`, `type`, `default`, `options`, `scope` and `description`, and three for this person. `title` and `summary`, since P11-04, are the setting's readable name and one short plain line on what it does ([settings.md](settings.md)); either is empty for a setting the registry gives none.

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

## The screens

`web/app/src/Settings.tsx` and `settings.css`. They are reached from the person's name at the foot of the sidebar, which is a link to `/settings`. Every server call they make is in `src/api.ts`, as for the rest of the app ([web.md](web.md)).

| Path | Screen |
| --- | --- |
| `/settings` | Goes to `/settings/profile`. |
| `/settings/profile` | The person's name, which cannot be changed yet, and the profile's text with a count against its limit. A sentence says the profile goes to every model, hosted ones too. |
| `/settings/appearance` | The theme switch: dark, light or system. |
| `/settings/models` | Keys and models. See below. |
| `/settings/general` | Every setting of the registry as a name, one short line and its value, with a control for each that is the person's to change. See "How General is drawn". |
| `/settings/connectors` | Since P7-04, each connector with its switch, its token, its tools and why it could not be reached, and a form to add a remote one. See [connectors.md](connectors.md). |
| `/settings/connectors/google` | Since P12-02, the Connectors screen with a Google section at its top: not set up and what the admin adds, Connect Google, connected as an account, Disconnect. Google sends the browser back to this address. See [google.md](google.md). |
| `/settings/approvals` | Since P7-02, each tool with the person's policy for it: always, ask or never. See [approvals.md](approvals.md). |
| `/settings/usage` | Since P11-02, what the person's model calls cost for a period against the monthly cap, by model, by chat and by kind of work. See [usage-report.md](usage-report.md). It took the place of the Spend placeholder. |
| `/settings/spend` | Goes to `/settings/usage`. |
| `/settings/operations` | Since P11-03, a tab that goes to the Operations page, `/operations`, which is wider than the settings column. See [operations.md](operations.md). Its twelve settings for a person (watch levels, windows and how much is listed) are drawn on General from the registry. |

The seven are tabs in a row under the title, and the row scrolls sideways on a phone.

### Models and keys

**Keys.** The stored names, each with the models that use it and a Remove button that asks once more. Below, a form with a name and a key. The key's field is a password field, it is emptied after every try, and the page keeps no copy: there is nothing to show again, because the server sends none back.

**Models.** One row for each: the label, the registry name, the adapter, the provider's model name, the window, the class, and whether the key it names is stored or missing. Edit and Remove are on each row. A stored entry the registry refuses today is shown with the reason and can only be removed.

The form adds a model or changes one: name, label, adapter, class, endpoint, model name, key name, context window, output limit, the two prices and the capabilities. The adapters, classes and capabilities come from `GET /api/v1/models`, so the form offers what the server takes. The key name field suggests the stored names. Choosing the `fake` adapter hides the endpoint, the model name and the key, and fixes the class at `local`, as the registry requires. Each class has a sentence under it saying what it means for sensitive content. A refusal is the registry's own reason, shown above the lists.

The form does not show `ca_cert`, `cache_read` or `cache_write`. An edit keeps the values an entry already has. The command line sets them ([adding-a-model.md](../runbooks/adding-a-model.md)).

Since P13-02 the routes take `ca_cert` as PEM text only: `POST /api/v1/models`, and `PUT` with a path the entry does not already hold, answer 400 `bad_model`, because the adapter reads a path with the server's own rights, so any person could have named another person's file or a share on another host. A path is set from the command line, and an edit that sends back the path an entry holds is taken (decision [0490](decisions/0490-a-ca-cert-path-is-set-from-the-command-line-only.md), `internal/settingsapi/cacert.go`).

### How General is drawn

General is generated from `GET /api/v1/settings` and has no code for any one setting. Since P11-04 it is drawn for reading at a glance, on a phone first: each setting is a name, one short line and its value, and the long explanation is one tap away, in place. Decision [0560](decisions/0560-a-setting-is-a-name-a-short-line-and-a-value-and-opens-in-place.md) says why, and what the two other sketches lost on.

The screen is three files in `web/app/src/`:

| File | Holds |
| --- | --- |
| `settingsView.ts` | What the screen works out before it draws: a setting's name and short line, its heading, what a search finds, how a value reads, and what a row shows closed and open. It imports nothing, so its tests run in Node against the real `settings/*.json`. |
| `GeneralSettings.tsx` | The drawing, and the calls that keep a change. It takes every word it shows from `rowOf` and `moreOf` of the file above. It names one setting, the theme, which the page wears at once. |
| `settings-general.css` | The rows, from the tokens. |

`SettingsParts.tsx` holds what every settings screen shares (how a refusal is said, how a screen reads what it shows, a section with its heading), which was in `Settings.tsx` until General got a file of its own.

**Above the list** are a search box, a select of the sections, and a Changed button with the number of settings that differ from their default. The search looks in names, short lines, keys (typed with dots and underscores, or with spaces) and headings, and every word typed must match. The three narrow the list together, a line says how many are left, and "Show all" clears them. Nothing takes the focus when the screen opens.

**The headings** are worked out from key prefixes by a list in `settingsView.ts`, not from the lane: `chats.context_` is under "What a model is sent", `knowledge.recall` under "Recall", `signin.` under "Signing in". The longest prefix that fits wins, the list's order is the screen's, and a key no prefix covers gets a heading made from its first word. Decision [0562](decisions/0562-headings-are-worked-out-from-key-prefixes-by-the-screen.md).

**A row, closed**, is one card row:

- the setting's `title`, and a small "Changed" mark when its value is the person's own and differs from the default;
- at the right, its value as plain text ("24h", "This month", "Off", "Empty"), and a chevron. A long text shows no value here;
- under them, its `summary`: one short plain line.

A setting the registry gives no title or summary still has a row: its name is made from its key, and its line is the first sentence of its description.

**A switch is the one control in a row.** A `bool` setting that is the person's is a switch at the right, and it is flipped where it is. Everything else is opened first.

**The whole row opens it.** The name is a button (`aria-expanded`, described by the short line) whose target is stretched over the row; the switch is a button of its own over it. Open, the row adds in place:

| Part | Shown |
| --- | --- |
| The explanation | The registry's `description`, whole. Left out when the description is the short line itself, as it is for thirteen settings whose description was already one short sentence, so the same sentence is not said twice. |
| The box | For a setting that is the person's and not a switch. See the table below. Under it, what it takes ("A whole number.") and "Saved." for a moment after a value is kept. |
| A value in full | For a setting that is not the person's, when the row cut its value short or left it out. |
| Who sets it | For a setting that is not the person's: the admin, for each person or for the whole install. |
| Default | The default as a value reads, and "Reset to default" when the value differs from it. |
| Key | The key in monospace, with a Copy button. This is the only place the key is shown. |

The box is chosen by `type` and `options` alone:

| The setting | The control | Kept when |
| --- | --- | --- |
| `bool` | A switch, in the row | It is clicked |
| has `options` | A select of the options, each read in plain words ("this_month" as "This month") | One is chosen |
| `int`, `float`, `duration` | A box of one line | The person presses Enter or leaves the box |
| `string` | A box of one line, or of several lines when the default or the value is longer than 60 characters | The same |

A change is one `PUT`, and a reset is one `DELETE`. The row then shows what the server answered. A refusal is shown in the row in the server's words.

So a setting added to the registry with the `tenant` scope has a row and a control the next time the screen is opened, with no change to the interface, under the heading its key's prefix has.

**On a phone** the tools wrap to two lines, a row is its name and value over its short line, and every box of the settings screens is 16px, so the browser does not zoom the page when one takes the focus. There is no overlay: nothing has to follow the keyboard.

Under the person's settings are two more parts. "Set for the whole install" holds every setting that is not the person's to change, as the same rows under the same headings with no control, behind a button until asked for; a search or a section shows the ones that match without it. "Fixed rules" holds the four rules, each marked "Always on", with no control.

### The theme

`appearance.theme` is a setting like any other, and it has a row on General. That row says the theme the page wears, so a choice made in the sidebar while General is open is shown at once. It is also what the theme switch sets, on Appearance and in the sidebar. Decision [0192](decisions/0192-the-theme-is-a-setting-once-chosen.md) says how the setting and the browser's own copy fit together:

- A choice made while signed in is worn at once, kept in the browser as before, and stored as the person's `appearance.theme`.
- At sign-in, the page reads the settings. A theme the person has stored replaces the browser's. A person who has stored none keeps what the browser has.
- Before sign-in the page wears the browser's copy, which `public/theme.js` applies before it paints.

### The content security policy

The screens have no inline script and no `style` attribute, and load nothing from another site. No package was added.

## The proof

The item's proof is "every overridable setting in the registry has a control". Because General is drawn from the route and from nothing else, the proof is a test of the route, `TestEveryOverridableSettingHasAControl` in `internal/settingsapi`, run by the `tests` check on every `verify`. On the real registry it shows that:

- every setting in the registry is in the route's answer, with the registry's type, default, lane, scope and description;
- every setting a person may change, by `tenantsettings.MayChange`, is marked `changeable`, has one of the five types the table above has a control for, and can be stored with each value its control offers and then reset, through the routes;
- every other setting is not marked `changeable`, and a write to it is refused;
- no fixed rule is in the list of settings, the four are listed apart word for word, and a write or a reset on a rule's key is refused;
- when it is done, nothing is stored.

Decision [0191](decisions/0191-the-general-screen-is-drawn-from-the-route.md) says why the proof is a test of the route and not a list kept by hand. The screen's side of it, that each `changeable` setting gets a row, was checked in a browser by comparing the rows on the page with the route's answer: 24 of 24 on November 25, 2026.

### The proof of P11-04

P11-04's proof is "every setting shows a short line of its own and its full explanation on request, at desktop and phone width, with nothing lost from the registry". It is in four places, three of them run on every `verify`:

- **The registry.** `TestEverySettingHasATitleAndASummary` in `settings`, and the `settings registry` check, run `settings.CheckWords` on the real registry: every setting has a title and a summary of the shape [settings.md](settings.md) gives.
- **The route.** `TestEveryOverridableSettingHasAControl` also shows that the route gives each setting the registry's title, summary and description.
- **The screen's reckoning.** `web/app/test/settingsView.test.ts` reads the real `settings/*.json` as the route does and shows, for every setting: its row's name is its title and its line is its summary, and the row does not carry the description; opened, it gives the description whole (or, when the description is its short line, does not say it again), its default and its key; it is in exactly one group of exactly one list, so nothing is unreachable; it is flipped in its row or has a box once opened if it is the person's, and neither if it is not; and the search finds it by its name, by the words of its short line and by its key. It also covers the headings, the Changed mark and filter, a setting with no title or summary, and how a value reads.
- **The screen itself**, in a browser, by comparing the page with the route's answer: 346 rows for 346 settings at 1280 pixels on November 27, 2026, each with its title and summary, none showing its description while closed and each showing it once opened; at 375 pixels the person's 146 rows were counted and a number of them opened and changed. After `reboot` was merged at 5f6d798, again at 375 pixels: 386 rows for the route's 386 settings, each with its title and summary, none showing its description closed, and each opened in turn by a script showing its description exactly once and its key. Nothing draws the page on every `verify`. The `settings at a glance` check holds the link between the tested file and the page instead: `GeneralSettings.tsx` must take its words from `rowOf` and `moreOf` and read no title, summary or description itself, must open a row in place with a button that says whether it is open, and must name no `autoFocus` and no `.focus(`; `settings-general.css` must set every box of the settings screens to 16px at phone width; and a fixed overlay may not be added without following `visualViewport`.

## Checked by

- `go test ./internal/settingsapi`.
  - `TestEveryOverridableSettingHasAControl` is the item's proof. See "The proof" below.
  - A person's override is theirs and not another person's. A value that does not fit, an unknown key and a fixed rule are refused, and a refusal does not repeat the value.
  - A secret is stored for the person and not for another, is listed by name, and its value is in no answer of any route.
  - A model is added, listed, replaced and removed. It says whether its key is stored, before and after the key arrives. A second model of the same name, one over the limit, a key pasted as a key name and a `local` class on an outside address are refused.
  - The profile is read, written, cleared, held to its limit, and is the person's own.
- The `no keys in output` check has a walk of its own for these routes, `tools/verify/settingsroutes.go`. It stores an invented value through `PUT /api/v1/secrets/{name}`, then calls the routes that could hand it back, and sends the value where it does not belong: as a secret's name, a model's key name and endpoint, a setting's value. The value must be in no answer, not in the server's log, and in no file of the data folder.
- The route walks in `tenant isolation`, `calls recorded` and `no keys in output` reach these routes with every other, because they read `app.Routes` ([tools.md](tools.md)).
- In a browser, as the test tenant, on a server started from this branch with a data folder of its own. The journal, interface 003, lists what was done there and what was not.
- Since P11-04: `go test ./settings` for the titles and summaries, `npm test` in `web/app` for `settingsView.ts`, and the `settings at a glance` check in `tools/verify/settingsglance.go`, which runs both and reads the screen's source ([tools.md](tools.md)). "The proof of P11-04" above says what each shows. The journal, interface 010, lists what was looked at in a browser and what was not.

## Not built yet

- Setting the monthly cap from the browser: it is the admin's (`tenant-admin`), and Usage shows it since P11-02 ([usage-report.md](usage-report.md)). Approvals has its list since P7-02, and Connectors its screen since P7-04 ([connectors.md](connectors.md)); a connector's token is stored with `PUT /api/v1/secrets/{name}` as `connector.<name>`.
- Changing a password, passkeys, and the list of signed-in devices.
- Changing the person's name, and a short name for the greeting.
- An admin: a role, a screen for the people on the install, and a way to change a `tenant-admin` or an `install` setting. Those settings are listed and cannot be changed from the browser.
- A default model picked from a list. `chats.default_model` on General is a text field for a model's registry name. A model added here can also be picked for one chat in the chat's composer, since P4-04's model picker ([web.md](web.md)).
- An address for one setting on General, so a link could open its row, and a way to open or close every row at once.
- Testing a key or a model from the screen. A wrong key is found when a chat uses the model.
- `ca_cert` and the cache prices in the model form.

## Provisional

- **The owner gate.** There is no settings mockup. `Admin.html` is a draft in the first round's colours and shows the admin's screens, not a person's. The cards, rows and pills follow it, the controls follow `Theme.html`, and the colours are the tokens. The tabs, the forms and every sentence on the screens were chosen without a mockup.
- **Who may change a model registry.** The owner's open question (`STATE.md`, models 003) is recommended as "the admin only". There is no admin role, so the routes and the screen let the signed-in person add, change and remove their own models, and mark one `trusted`. If the answer is the admin only, the three writing routes get a role check and the form goes to the admin's screen. The list stays.
- **Standing instructions.** `chats.context_system_text` has the `tenant` scope, so it has a control on General, as the owner's open question (chats 003) recommends. If the answer is the admin only, the setting's scope changes in `settings/chats.json` and the control goes with it, with no change here.
- **The profile goes to every model.** The screen says so in a sentence, as the open question (knowledge 004) recommends.
- **The look of General (P11-04).** The rows, the Changed mark, the tools above the list, the 34 headings and every title and summary were chosen without a mockup, and the owner has not yet seen them on his phone. Decision [0560](decisions/0560-a-setting-is-a-name-a-short-line-and-a-value-and-opens-in-place.md) records the three sketches and the choice.
- **Install settings are shown to every person.** A person sees the value of every setting, the admin's too, and none of them holds a secret. See "For the owner" in the journal.
