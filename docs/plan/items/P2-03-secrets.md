# P2-03. Per-tenant secrets

Status: done
Lane: core
Depends on: P2-01
Owner gate: none
Scope: A data key per tenant, wrapped by an install master key held by the OS (DPAPI on Windows, keychain elsewhere). secrets.bin holds provider keys and connector tokens.
Proof: secrets.bin cannot be read without the master key; the no-keys-in-output check is added to verify.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/secrets`. A `Keeper` wraps and unwraps a tenant's data key, bound to a context that names the tenant. A `Vault` is one tenant's `secrets.bin`: set, get, list names, delete.
- `secrets.bin` holds the wrapped data key at its head and the secrets after it, encrypted with AES-256-GCM from the standard library. Both the wrap and the encryption are bound to the tenant id. Writes go to a temporary file and are renamed into place. A file that cannot be read is refused and left as it is.
- Keepers: DPAPI on Windows through `golang.org/x/sys/windows`, the login keychain on macOS, the Secret Service on Linux, each behind a build tag. A key file in the user's profile for a system with none, used only when the install is told to. One held in memory for tests.
- `internal/tenant`: `Open`, `Create` and `Store` keep their signatures. `OpenWithKeeper` is added for tests and for the tenant command, and `Store.Secrets()` returns the tenant's vault. The keeper is found the first time a secret is used, and `keystone.json` then records which keeper it is, never a key.
- One setting, `secrets.keeper`: `system` or `file`.
- `tools/tenant secret set | list | delete`. The value is read from standard input, never from an argument, and is never printed.
- The standing check `no keys in output`, in `tools/verify/nokeys.go`: store an invented value, then look for it in everything the package returns, everything the tenant command prints, and every file in the data folder. Later items add the logs, the audit log and the API responses to its list.
- Proof: tests that `secrets.bin` does not hold the plain value, that another master key cannot read it, that one tenant's file is refused in another tenant's folder, and that a damaged file is refused and not replaced. A Windows-only test for the DPAPI keeper.
- Docs: `docs/architecture/secrets.md`, a row in the overview, decision 0008 for the keepers on macOS and Linux and for a system with none, and a runbook for storing a key and for what to do after a restore.


### Result

Done on November 24, 2026. Built as planned, with two changes made when the lanes were merged.

- `internal/secrets` holds the vault and the keepers, and `Store.Secrets()` returns a tenant's vault. `tools/tenant secret set | list | delete` stores, lists and removes. `verify` has the `no keys in output` check.
- The proof holds: the tests show `secrets.bin` does not hold the plain value, another master key cannot read it, one tenant's file is refused in another's folder, and a damaged file is refused and not replaced. The DPAPI keeper is tested on Windows.
- Changed at the merge: `internal/tenant` no longer reads the `secrets.keeper` setting. P2-04's fixed rules check refuses a guard of tenant isolation that can read a setting. The tenant command reads the setting and passes it in through `Install.NewInstallKeeper`.
- The lane's session stopped after its code was committed. The doc, decision 0008, the runbook and the journal entry were written by the integrator.
- Provisional: the macOS keychain keeper and the Linux Secret Service keeper compile and share tested code, and have not been run on those systems.

Journal: `docs/journal/2026-11-24-core-006.md`.
