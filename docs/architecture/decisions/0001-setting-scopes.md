# 0001. Who may change a setting is a scope with three values

November 24, 2026 · decided in P0-01

## The question

The plan's first item asked the registry to record "whether a tenant may override" each setting. A yes or no is not enough. The monthly spend cap is different for each person and is set by the admin, not by the person. A yes would let a person raise their own cap. A no would force one cap on everyone.

## The decision

Each setting has a scope:

- `install`: one value for the whole install, set by the admin.
- `tenant-admin`: one value for each person, set by the admin.
- `tenant`: one value for each person, set by that person.

A person may override a setting only when its scope is `tenant`.

## What it costs

One more value to choose when adding a setting. Changing a setting's scope later is a one-line change in its lane file, plus moving any stored values.
