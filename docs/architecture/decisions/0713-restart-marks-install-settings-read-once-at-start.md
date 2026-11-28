# 0713. The registry's restart field marks the install settings the server reads once, at start, and a test holds the marks

November 28, 2026 · decided in P15-03

## The question

The spec adds an optional `restart` field to the registry "for a setting that takes effect only after a restart". Which settings get it, how is a change to one handled while the server runs, and how are the marks kept true as lanes add settings?

## The decision

- `restart` is a bool on a registry entry, left out when false, and allowed on an `install` setting alone (a person's setting is read when it is used); `settings.Validate` refuses it elsewhere.
- Marked: every install setting `app.ReadConfig` reads into the server's configuration at start (133 when this was built), the ones `keystone serve` reads before it serves (`server.address`, `data.dir`, `secrets.keeper`, the three `encryption.` settings), and the model adapters' (`anthropic.`, `chat_completions.`, `responses.`, `systemone.`, `fake.` and `models.max_per_tenant`), which `adapters.Real` reads once. 172 in all, each as one added line, `"restart": true`, above its `scope`.
- Not marked: an install setting read through `tenantsettings` (the turn runner's `r.setting`, the router's settings) or through a fresh `settings.Registry()` at each use. Those follow a change at once (decision 0711).
- A change to a restart setting is written to `keystone.json` and does not reach the overlay: the overlay keeps the value the server started with until the next `Load`, so no part runs half on the old value and half on the new. The screens show it as "Waits for a restart", with what Keystone runs with until then.
- `TestEveryInstallSettingReadAtStartIsMarkedRestart` in `internal/app` fails when `app.ReadConfig` reads an install setting that is not marked, naming the lane file.

## Why

- The configuration read at start is the one place most install settings are captured; a test on it is exact and catches the next setting a lane adds there.
- Holding the overlay for restart settings keeps the process consistent: the parts that read the registry again would otherwise see a value the parts that captured it at start do not.

## What it costs

- The adapters' and `keystone serve`'s marks are by hand, from reading the code; no test holds them.
- 172 added lines across seven lane files, in other lanes' entries. Each is a line of its own inside an entry, so it merges unless the same entry's `default` or `scope` line changed beside it.
