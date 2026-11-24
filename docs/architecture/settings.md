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

## What is refused

Reading the registry fails, and so does `verify`, when a file's name and its `lane` disagree, a key is defined twice, a file has a field the registry does not know, a default does not read as its type, or a description is missing.

## Not built yet

Stored values and overrides. This item defines what can be set. Saving an install's or a person's values, and checking them with `CheckValue`, comes with the tenant store (P2-01 and P2-04).
