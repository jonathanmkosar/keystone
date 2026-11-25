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
| `type` | `bool`, `int`, `float`, `string` or `duration`. |
| `default` | Written as text and read as the type. Chosen to give the best experience with no setup. |
| `options` | For a string with a fixed set of values. The default must be one of them. |
| `scope` | Who may change it. See below. |
| `description` | One or more full sentences. This is what the settings screen shows. |

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

## What is refused

Reading the registry fails, and so does `verify`, when a file's name and its `lane` disagree, a key is defined twice, a file has a field the registry does not know, a default does not read as its type, a description is missing, or a key is in the space kept for the fixed rules.

## Stored values

A tenant's own values are kept in its database by `internal/tenantsettings`. See [tenant-settings.md](tenant-settings.md).

## Not built yet

The install's own stored values, for settings with the `install` scope. Today those have their defaults, and `keystone serve` takes its address from a flag.
