# Settings registry

Folder: `settings`

Every value an owner or a person might change is a setting, not a constant in code. The registry is the list of all of them. `keystone settings` prints it.

## One file per lane

Each lane keeps its settings in its own JSON file: `settings/core.json`, `settings/interface.json`, and so on. Lanes work at the same time, and separate files mean they never edit the same one. The files are compiled into the binary and merged when the registry is read.

```json
{
  "lane": "core",
  "settings": [
    {
      "key": "server.address",
      "title": "Server address",
      "summary": "The address and port Keystone listens on.",
      "type": "string",
      "default": "127.0.0.1:7900",
      "scope": "install",
      "description": "The address and port Keystone listens on. The default is reachable from this machine only."
    }
  ]
}
```

## What a setting has

| Field | Meaning |
| --- | --- |
| `key` | Lower-case words joined by dots, such as `spend.monthly_cap`. Unique across every lane. |
| `title` | The setting's readable name, such as "Budget for each scheduled run". Since P11-04. See "Titles and summaries". |
| `summary` | One short plain line on what the setting does, such as "The most one scheduled run may spend on paid models." Since P11-04. |
| `type` | `bool`, `int`, `float`, `string` or `duration`. |
| `default` | Written as text and read as the type. Chosen to give the best experience with no setup. |
| `options` | For a string with a fixed set of values. The default must be one of them. |
| `scope` | Who may change it. See below. |
| `description` | One or more full sentences: the whole explanation. The settings screen shows it once a setting's row is opened. |

## Who may change a setting

| Scope | One value for | Changed by |
| --- | --- | --- |
| `install` | The whole install | The admin |
| `tenant-admin` | Each person | The admin |
| `tenant` | Each person | That person |

A person may override a setting only when its scope is `tenant`. The four fixed rules in the plan are not settings and cannot be switched off by one.

## The fixed rules

The plan has four rules no setting can switch off. Decision [0014](decisions/0014-fixed-rules-and-tenant-settings.md) gives them a form the code can test.

`settings.FixedRules()` lists the four, each with a key and its text, word for word from `PLAN.md`:

| Key | Rule |
| --- | --- |
| `rules.tenant_isolation` | A tenant never reads another tenant's data. |
| `rules.approval` | A side effect outside Keystone happens only under the approval policy, and content the agent read never grants approval. |
| `rules.sensitive_content` | Content from a sensitive space reaches only models the registry marks local or trusted. |
| `rules.call_recording` | Every model call and every tool call is recorded: what was sent, what came back, tokens and cost. |

A rule is not a setting. It has no value, no default and no scope, so there is nothing to set. Its key only names it, in a part of the key space, `rules.`, that is kept for the rules:

- The registry refuses a setting whose key starts with `rules.`.
- Tenant settings refuse to store a value for such a key, and so does the table underneath.
- The code that enforces a rule lives in a package that does not import the registry or the tenant settings, so it has no way to read a value that could switch the rule off.

The `fixed rules` check in `verify` proves all three on every run, and that the list still matches the plan. See [tools.md](tools.md).

## Titles and summaries

The settings screen shows a setting by its `title` and its `summary`, and keeps its `description` one step away ([settings-screens.md](settings-screens.md)). Every setting has both, each on a line of its own directly under the entry's `key` line, so two lanes that add settings side by side merge without a conflict.

| Field | Must be |
| --- | --- |
| `title` | 1 to 40 characters. It starts with a capital or a digit, does not end in a full stop, has no key in it, and is no other setting's title. |
| `summary` | 3 to 14 words and 80 characters at most. It ends in a full stop and has no key and no `{placeholder}` in it. |

A title is a name, not a sentence: "Budget for each scheduled run", not "Schedule budget usd". A summary says what the setting does in the app's voice, in about ten words, and leaves the ranges, the exceptions and the other settings it touches to the description.

Reading the registry does not ask for either, so a setting added without them still loads and still has a row on the screen, named from its key, with the first sentence of its description. `settings.CheckWords(reg)` is what asks: it lists every setting whose title or summary is missing or does not fit, with the file it is in and what to add. `verify`'s `settings registry` check runs it, and `go test ./settings` does too. Decision [0561](decisions/0561-a-settings-title-and-summary-are-in-the-registry-and-a-check-asks-for-them.md) says why it is a check and not a refusal.

## What is refused

Reading the registry fails, and so does `verify`, when a file's name and its `lane` disagree, a key is defined twice, a file has a field the registry does not know, a default does not read as its type, a description is missing, or a key is in the space kept for the fixed rules. `verify` fails, and reading does not, when a title or a summary is missing or does not fit.

## Stored values

A tenant's own values are kept in its database by `internal/tenantsettings`. See [tenant-settings.md](tenant-settings.md).

## Not built yet

The install's own stored values, for settings with the `install` scope. Today those have their defaults, and `keystone serve` takes its address from a flag.
