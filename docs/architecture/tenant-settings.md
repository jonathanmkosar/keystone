# Tenant settings

Folder: `internal/tenantsettings`

The [settings registry](settings.md) says what can be set, each default, and who may change it. This part stores what one tenant has changed, in that tenant's own database, and answers what value is in force. Decision [0014](decisions/0014-fixed-rules-and-tenant-settings.md) says why it is a package of its own.

A setting with no stored value has its default.

## Who may change what

Decision [0001](decisions/0001-setting-scopes.md) gives each setting a scope. A change is asked for by a role, `person` or `admin`.

| Scope | `person` | `admin` |
| --- | --- | --- |
| `tenant` | May change it | May not: it is the person's own |
| `tenant-admin` | May not | May change it, for each tenant |
| `install` | May not | May not: it has one value for the whole install, never one per tenant |

`MayChange(setting, role)` is this table as a function. The four fixed rules are not settings: asking to change one is refused for either role, before the registry is even read.

## The functions

Each takes the tenant's store handle and the registry. Passing the registry keeps the package plain, and lets a test use a made-up one.

- `Set(ctx, store, reg, role, key, value)` stores a value. The role must be allowed to change the setting, and the value must pass `settings.CheckValue`: it reads as the setting's type and is one of its options.
- `Clear(ctx, store, reg, role, key)` removes the stored value, so the setting goes back to its default.
- `Get(ctx, store, reg, key)` returns the value in force, as text.
- `All(ctx, store, reg)` returns every setting in the registry as it stands for the tenant: the value in force, whether it is the tenant's own, and who stored it and when. The settings screens (P11-01) are built on it.

A refusal is one of three errors, which the API turns into a status: `ErrUnknown` for a key that names no setting, `ErrNotAllowed` for a role that may not change it or a key that names a fixed rule, and `ErrBadValue` for a value that does not fit.

## When the registry changes

A stored value counts only while whoever stored it could store it today and it still passes `CheckValue`. If a setting's scope, type or options change in a later version, a value that no longer fits falls back to the default. The row is kept, so nothing has to be found and removed, and changing the registry back brings the value back.

## The table

`tenant_settings` has one row for each stored value: the key, the value as text, who set it and when. The table refuses a key under `rules.`, the part of the key space kept for the fixed rules, so no code can store a value for one even by writing SQL.

Structured data a tenant holds, such as the model registry (P3-02), does not go in this table. It gets a table of its own in its own migration, and can use `MayChange`'s roles for who may edit it. `tenant_settings` holds one text value for one registry key.

## Checked by

`go test ./internal/tenantsettings`.

- A person's override is returned for that tenant, not for another, and is there after the database is reopened. Clearing it brings the default back.
- Each scope and role in the table above is tried. A refused change stores nothing.
- A value of the wrong type, a value outside the options and an unknown key are each refused.
- Each of the four fixed rules is tried by both roles, with the real registry and with a forged one that lists the rule as a setting, and straight into the table. All are refused.
- A stored value that no longer fits a changed registry falls back to the default.

## Not here yet

- The install's own stored values, for settings with the `install` scope. `keystone serve` takes its address from a flag, and the data folder is the default.
- Routes to read and change settings. They come with the API shell (P2-05). A change made through the API is recorded in the audit log like any other call.
- Values read as their type. `Get` returns text, and a caller reads it as the registry's type says.
