# Secrets

A tenant's secrets are its provider keys and connector tokens. The code is in `internal/secrets`, with the join to a tenant in `internal/tenant/secrets.go`. The design is decision [0006](decisions/0006-secrets-wrapped-by-the-os.md), and the keepers for each operating system are decision [0008](decisions/0008-keepers-for-each-system.md).

## Three layers

1. **The secrets.** Names and values, as one JSON object, encrypted with the tenant's data key using AES-256-GCM. They are stored in `secrets.bin` in the tenant's folder.
2. **The tenant's data key.** 32 random bytes, made the first time the tenant stores a secret. It is stored only wrapped, at the head of the same `secrets.bin`.
3. **The install master key.** It wraps every tenant's data key. The operating system holds it, behind a `Keeper`. It is never written to the data folder.

So a copy of the data folder alone cannot be read. It takes the data folder and the account's key store together. A backup made with `tenant backup` is the way to move a tenant's keys on purpose: see "Moving a tenant's keys to another install" below.

Both the wrap and the encryption are bound to the tenant id. A `secrets.bin` copied from one tenant's folder to another's is refused, and so is a wrapped key moved between files.

## The file

`secrets.bin` is a four-byte magic, the length of the wrapped data key as two bytes, the wrapped data key, and then the encrypted secrets. Each write goes to a temporary file beside it and is renamed into place, so a crash never leaves half a file. A file that cannot be opened is an error and is left as it is: Keystone never replaces a file it could not read.

## The vault

`Store.Secrets()` returns the tenant's `Vault`. Like the rest of the store handle, it reaches that tenant's file and no other.

| Call | What it does |
| --- | --- |
| `Set(name, value)` | Stores a value, replacing any earlier one. |
| `Get(name)` | Returns the value and whether there is one. This is the only call that returns a value. |
| `Names()` | Lists the names, sorted. Never a value. |
| `Delete(name)` | Removes one. Removing one that is not there is not an error. |

A name is 1 to 100 lower-case letters, digits, dots, dashes and underscores, such as `provider.anthropic`. A connector's token is stored as `connector.<name>` by the Connectors screen, and is read for each session with its server and sent nowhere else (since P7-04, [connectors.md](connectors.md)).

Since P12-02, a person's Google sign-in is stored as `connector.google` (the refresh token, written by Connect Google), and the owner's OAuth client secret as `google.client_secret` in each person's vault (written with `tenant secret set`). Both are read in `reachFor` for each call to Google, and access tokens are kept in memory only ([google.md](google.md)).

Every handle for one tenant shares one vault, so two writers cannot lose each other's change. Nothing in the package prints or logs a value, and no error carries one.

## Keepers

A `Keeper` wraps and unwraps a data key. There is one for each place the master key can live.

| Name | Where the master key is | Used on |
| --- | --- | --- |
| `dpapi` | Windows data protection, bound to the signed-in account. Nothing is stored by Keystone. | Windows |
| the keychain keeper | A generic password in the login keychain, reached through `/usr/bin/security`. | macOS |
| the Secret Service keeper | GNOME Keyring or KWallet, reached through `secret-tool`. | Linux |
| `file` | `keystone/master.key` in the user's profile folder, outside the data folder. Weaker: whoever can read the file can unwrap every data key. | Any system with no key store, and only when asked for |
| `memory` | The process, and gone when it ends. | Tests |

`secrets.System()` returns the operating system's keeper. `secrets.Named` returns one by name.

## Which keeper an install uses

The keeper is found the first time a secret is used, not when the data folder is opened. An install that stores no secret never touches the key store.

- If `keystone.json` records a keeper, that one is used. A different keeper is refused, because it could not unwrap what is stored.
- If it records none, the caller says where a new install keeps its key by calling `Install.NewInstallKeeper` with the `secrets.keeper` setting: `system` or `file`. After the first data key is wrapped, `keystone.json` records the keeper's name. It never records a key.

`internal/tenant` enforces tenant isolation, which is a fixed rule, so it reads no setting itself. The command that opens the install reads `secrets.keeper` and passes it in. The fixed rules check in `verify` fails if that changes.

## The command

    Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.name
    go run ./tools/tenant secret list <tenant-id>
    go run ./tools/tenant secret delete <tenant-id> provider.name

The value is read from standard input and never from an argument, which would stay in the shell's history. It is never printed. The steps are in the [secrets runbook](../runbooks/secrets.md).

## The standing check

`verify` has a check named `no keys in output`, in `tools/verify/nokeys.go`. It stores an invented value as a tenant's secret, then looks for that value in everything Keystone printed, returned or wrote while it held it: what the package returns, what the tenant command prints, and every file in the data folder. The only place the value may come back is the call that asks for it by name. An item that adds a place a key could reach, such as the logs or the API's responses, adds a line to the check's list.

## Moving a tenant's keys to another install

Since P13-04 a backup carries a tenant's secrets to another machine ([backup.md](backup.md), decision [0261](decisions/0261-a-backup-carries-the-data-key-re-wrapped.md)). The secrets are not opened and sealed again: the data key is wrapped again.

| Call | What it does |
| --- | --- |
| `Rewrap(file, tenant, from, to)` | Unwraps the data key of a secrets file with `from`, opens the secrets to check them, and returns the file with the same data key wrapped by `to`, and how many secrets it holds. A file that does not open is refused before `to` is asked for anything. |
| `Vault.Rewrapped(to)` | The same for the vault's own file, read under its lock. Says when there is no file. |
| `KeyKeeper(name, key)` | A keeper that wraps with 32 bytes the caller made. A backup file is one, with a key made from its passphrase. |

A backup re-wraps the data key from the install's keeper to the backup's; a restore re-wraps it from the backup's keeper to the new install's. No unwrapped key leaves this package.

## Not built yet

- The keychain and Secret Service keepers have been compiled and their shared code tested, but they have not been run on a Mac or a Linux desktop. They are provisional until they are.
- Nothing rotates a data key or the master key.
- The API does not read or write secrets yet. That comes with the registry routes, after P2-05.
