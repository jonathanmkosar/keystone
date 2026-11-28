# 0711. The install's values reach the code through one overlay on the registry

November 28, 2026 · decided in P15-03

## The question

Every part reads its settings from the registry's defaults (`settings.Registry()` and `st.Default`), and a person's through `tenantsettings`, which falls back to the default. More than fifty call sites read the registry, several keep it for the life of the server, and a few read a setting once into a configuration at start. How does an install value reach all of them without changing each?

## The decision

- `settings.UseInstalled(values)` sets the install's values, by key, for the process. `internal/installsettings` calls it at start (`Load`, from `keystone serve` before any setting is read) and after each change.
- `settings.Registry()` gives each install value as its setting's `Default`. `settings.Shipped(st)` still gives the lane file's default.
- `settings.DefaultFor(st)` answers the default in force now, for a `Setting` read before the last change: code that kept the registry from the start sees a later change through it.
- `tenantsettings.Get` and `All` fall back to `DefaultFor`, not to `st.Default`. So every install setting and every person's setting read through `tenantsettings` (the turn runner's, the router's, the settings screens') follows a change at once, whatever registry it was handed.
- Only an `install` setting and a person's own (`tenant`) setting take an install value. A `tenant-admin` setting is set for one person, and a fixed rule is not a setting; `DefaultFor` and `Registry` pass both over, as they pass over a value that does not fit.
- A process that never calls `UseInstalled` (verify, the tests, the command line) sees the lane files' defaults.

## Why

- One overlay, read where every default is already read, changes no other lane's code and cannot be bypassed by a part that reads the registry its own way.
- `DefaultFor` makes the registries kept for a server's life correct without asking their owners to read the registry again.

## What it costs

- A process-wide value. One server serves one install, so that is what it means; a test that sets it clears it after (`t.Cleanup(func() { settings.UseInstalled(nil) })`).
- A part that copies `st.Default` into a configuration once does not see a change: that is what the `restart` mark says (decision 0713).
