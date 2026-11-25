# P13-04. Backup and restore

Status: done
Lane: core
Depends on: P2-03
Owner gate: none
Scope: Encrypted per-tenant backup, off by default; restore on another machine.
Proof: A tenant restored on a clean machine signs in and its chats and keys work.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan, written before the code (core 010, branch `core/p13-04-backup-restore`):

- **One file per tenant, under a passphrase.** A new package, `internal/backup`, holds the file format and nothing about tenants: a short header (magic, the key derivation and its rounds, a salt, a passphrase check), then the contents in 64 KiB chunks sealed with AES-256-GCM, each chunk's nonce a counter with a last-chunk flag so a cut or reordered file is refused. The key comes from the passphrase through PBKDF2-SHA-256 from the standard library, as passwords do (decision 0007); the file records its rounds. Inside the chunks: a gzip-compressed tar of `manifest.json` and the tenant's folder under `tenant/`. Names are checked on the way in and out: plain relative paths, regular files and folders only.
- **The data key travels re-wrapped.** `internal/secrets` gains `Rewrap` and `Vault.Rewrapped`: a secrets file's data key is unwrapped by one keeper and wrapped by another, and the secrets themselves are opened only to check them. A backup re-wraps it under a key derived from the passphrase; a restore re-wraps it under the new install's keeper. No raw key leaves the secrets package.
- **Backup and restore in `internal/tenant`** (it owns the folders and the registry; it still reads no setting). `Install.Backup(id, w, passphrase, options)` takes the database with `VACUUM INTO`, so it can run beside a running server, then the re-wrapped `secrets.bin` and every other file in the tenant's folder. `Install.Restore(file, passphrase, options)` reads the whole file once to check it (every chunk, the manifest, the names, the migrations this binary knows) before writing anything, re-wraps the data key, then extracts into a staging folder inside the data folder, opens and checks the database there, proves the secrets open under the new keeper, runs the caller's hook (the command signs out every old session), and only then renames the folder into place and adds the registry entry. It refuses an existing tenant unless told to replace it; a replace moves the old folder aside and puts it back if anything fails.
- **Commands in `tools/tenant`:** `backup <tenant-id> <file>` (passphrase from standard input, asked twice when typed; never an argument; the file is written beside its name and renamed, then read back and checked) and `restore [--replace] [--check] <file>`. Two settings in `settings/core.json`: `backup.passphrase_rounds` and `backup.passphrase_min_length`.
- **Scheduled backups are left for later.** A schedule needs a passphrase nobody types, which the brief forbids storing, or a public-key recipient; and the jobs package is being built now (P8-01). A decision record says so. Backups are therefore off by default: one is made only when a person runs the command.
- **Proof:** a test in `tools/tenant` backs up a tenant with chats, a file and a stored secret, restores it into a fresh data folder with another memory keeper, signs in, reads the chats and gets the secret. Package tests cover the refusals: wrong passphrase, a changed byte, a cut file, an existing tenant, a replace, bad names, a newer migration, and that a failed restore leaves no folder and no registry entry. `verify`'s `no keys in output` check gains a surface: a backup and a restore through the command with two key files, the stored value looked for in the backup file, everything printed and both data folders.
- **Write-up:** `docs/architecture/backup.md`, the runbook `docs/runbooks/backup-and-restore.md`, the "After a restore" section of `docs/runbooks/secrets.md`, decisions from 0260, journal core 010.

Result (core 010): done, built as planned.

- `internal/backup`, `internal/secrets/rewrap.go`, `internal/tenant/backup.go` and `tools/tenant/backup.go`; decisions 0260 to 0263; `docs/architecture/backup.md` and `docs/runbooks/backup-and-restore.md`. `PLAN.md`'s line on backups now says what a backup is.
- The proof passes in `go test ./tools/tenant` (`TestABackupRestoredOnAnotherMachine`): restored into a fresh data folder under another keeper, the tenant signs in with its password, its old session is refused, its two chats list, read back, are found by search and hold their file, and its key opens. `verify` proves it again through the built command with two key files, in `no keys in output`.
- By hand on Windows 11, with the real keeper (DPAPI) in throwaway folders: a backup of a tenant with an invented key and password took 0.6 seconds with the shipped rounds and was 10 KB; restored into a second data folder it listed its key, refused a second restore, and replaced the tenant in the first folder with `--replace`; served on its own port, the restored tenant signed in with `keystone signin` and `GET /api/v1/secrets` listed its key.
- Not built: scheduled backups (decision 0263), so a backup is made only when a person runs the command, which is what "off by default" asks. Not run: a restore on a second physical machine, or on macOS or Linux.

