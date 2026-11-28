# Backup and restore

Folders: `internal/backup` (the file), `internal/tenant/backup.go` (what goes in and how a tenant comes back out), `internal/secrets/rewrap.go` (the data key moved between keepers), and the commands in `tools/tenant/backup.go`. Built in P13-04. The steps for a person are in the [runbook](../runbooks/backup-and-restore.md).

A backup is one tenant in one file, sealed under a passphrase the person gives. A restore brings that tenant into a data folder, on this machine or another, with its keys under that install's own master key. Decisions [0260](decisions/0260-a-backup-is-one-file-sealed-under-a-passphrase.md) (the file), [0261](decisions/0261-a-backup-carries-the-data-key-re-wrapped.md) (the keys), [0262](decisions/0262-a-restore-is-all-or-nothing.md) (the restore) and [0263](decisions/0263-scheduled-backups-wait.md) (no schedule yet).

## The file

`internal/backup` knows nothing about tenants. It seals entries and checks them on the way back.

| Part | Size | What it is |
| --- | --- | --- |
| magic | 4 bytes | `KSB1`. The digit is the format's version. A higher digit is refused with "made by a newer Keystone". |
| kdf | 1 byte | `1`, PBKDF2-SHA-256. |
| rounds | 4 bytes | The rounds the passphrase went through, big-endian, 100,000 to 50,000,000. |
| salt | 32 bytes | Random, new for each file. |
| check | 32 bytes | Made from the key, so a wrong passphrase is told from a damaged file. |
| chunks | the rest | The contents, 64 KiB at a time, each sealed with AES-256-GCM. |

The passphrase and the salt go through PBKDF2-SHA-256, and HKDF-SHA-256 makes three keys from the result: the contents key, the key that wraps a tenant's data key, and the check. A chunk's nonce is its number and a flag for the last chunk, and every chunk carries the header as additional data. A reader reads one byte past each full chunk to learn whether it is the last, so a file cut exactly at a chunk's edge is refused: its last chunk was sealed as one that is not. `Next` returns `io.EOF` only when the last chunk has opened and nothing follows it.

The contents are a gzip-compressed tar. An entry is a regular file or a folder, named by a plain relative path joined with `/`. `CheckName` refuses an absolute path, `..` or `.`, an empty part, a backslash, a colon, a control character, a name that is not UTF-8, and a name the system reserves (such as `NUL` on Windows). The writer refuses such a name, so every backup it writes can be restored, and the reader refuses it again, with links, devices and any name twice, in either case.

| Call | What it does |
| --- | --- |
| `Create(w, passphrase, rounds)` | Writes the header and returns a `Writer`. |
| `Writer.File(name, size, time, r)`, `Writer.Dir(name, time)` | Add an entry. |
| `Writer.Keeper()` | A `secrets.Keeper` that wraps under this file's key. |
| `Writer.Close()` | Writes the last chunk. Until then the file is cut short and refused. |
| `Open(r, passphrase)` | Reads the header and makes the key. A wrong passphrase is `ErrPassphrase`. |
| `Reader.Next()`, `Reader.Read(p)` | Each entry and its bytes. Every error about the contents wraps `ErrDamaged`. |
| `Reader.Keeper()` | Unwraps what the writer's keeper wrapped. |
| `Reader.Reopen(r)` | Reads the same file again with the key already made. Another file is refused. |

## What a tenant's backup holds

| Entry | From |
| --- | --- |
| `manifest.json` | Always first: the format and version, when the backup was made, the tenant's registry entry (id, name, created), the migrations its database has had, and whether it has secrets. |
| `tenant/keystone.db` | The database, copied by SQLite with `VACUUM INTO` as one consistent snapshot, so a backup can run while the server runs. The copy is made in a `.backup-…` folder inside the data folder and removed afterwards. |
| `tenant/secrets.bin` | The secrets file, with its data key wrapped by the backup's own key instead of the install master key (`Vault.Rewrapped`). Only when the tenant has stored a secret. |
| `tenant/…` | Every other file and folder in the tenant's folder, as it is: `files/`, `artifacts/`, and whatever a later item adds. The database's journals and the secrets file's temporary file are left out. A link, or anything that is not a file or a folder, stops the backup. |

`Install.Backup(id, w, passphrase, options)` writes it and returns a `BackupInfo`: the tenant, when it was made, how many files and bytes, and how many secrets. It never names a secret. `BackupOptions` carries the rounds and the shortest passphrase, which the command reads from the settings: `internal/tenant` guards tenant isolation and reads no setting.

Since P15-06, a sealed person's backup holds their bytes as they are on disk: the snapshot is written through the VFS under the same keys, and `seal.json` and the sealed files are copied as they are. A restore opens the folder with the keys from its own secrets file, under this install's keeper, before it is put in place, so the person comes back sealed; a plain backup restores plain and is sealed at the server's next start. The server also writes a backup of each person just before it seals them for the first time, under a random passphrase kept beside it wrapped by the install master key ([encryption.md](encryption.md), decisions [0663](decisions/0663-an-existing-install-is-sealed-at-start-one-person-at-a-time.md) and [0664](decisions/0664-a-sealed-persons-backup-carries-the-sealed-bytes.md)).

## Restore

`Install.Restore(file, passphrase, options)` is all or nothing (decision 0262).

1. The whole file is read and checked, and nothing is written: every chunk, the manifest, every name, that this binary knows every migration the database has had, and that the secrets open with the backup's keeper. A tenant already on the install is refused as soon as the manifest is read, with `ErrTenantExists`, unless `Replace` is set.
2. The data key is wrapped by this install's keeper (`secrets.Rewrap`). On a new install this is what makes the master key and records the keeper in `keystone.json`.
3. The file is read again into `tenants/.<id>.restore-…`. It must hold the same entries and hash to the same SHA-256, or the restore stops.
4. The database there is opened, brought up to date by the migrations, checked by `PRAGMA quick_check` and checked to belong to this tenant. `restored_at` and `restored_from_backup_made_at` go into `tenant_meta`. The secrets must open under the new keeper, and `Ready` runs on the store.
5. The folder is renamed to `tenants/<id>` and the tenant is added to the registry. A replace moves the old folder aside first and puts it back if the rename or the save fails, and removes it only after the save.

Any failure removes the new folder. `CheckBackup(file, passphrase)` runs step 1 alone, for `restore --check` and for the command's read-back after a backup.

The registry is kept in memory by the server and written back whole, so a restore is run with the server stopped. A backup can run beside it.

## The commands

    Get-Clipboard | go run ./tools/tenant backup <tenant-id> <file>
    Get-Clipboard | go run ./tools/tenant --data <folder> restore [--replace] [--check] <file>

The passphrase is read from standard input, one line, never from an argument, and stored nowhere. Typed at the keyboard it shows on the screen, and `backup` asks for it twice. `backup` refuses a file that is already there; it writes `<file>.partial`, reads it back with the passphrase, and only then renames it. `restore` uses the install's keeper, or for a new install the one `secrets.keeper` names, and ends every session the restored tenant had (`signin.SignOutAll` as `Ready`). Nothing either prints carries a key or the passphrase.

| Setting | Default | What it does |
| --- | --- | --- |
| `backup.passphrase_rounds` | 2000000 | The rounds of PBKDF2-SHA-256. A backup records its own count. |
| `backup.passphrase_min_length` | 12 | The fewest characters a passphrase may have. |

## Checked by

- `go test ./internal/backup`: a round trip of several chunks; the two keepers match; a wrong passphrase; a changed byte at seven places, a file cut at chunk edges, inside a chunk and by one byte, a byte and a chunk added, a chunk dropped, swapped or repeated, and a changed header, each refused; names that leave the folder, links, devices and names twice, refused by the writer and by the reader of a hand-made file.
- `go test ./internal/secrets`: `Rewrap` moves the same data key to another keeper, refuses a file it cannot open before the new keeper is asked for anything, and `KeyKeeper` keeps its key out of print.
- `go test ./internal/tenant`: a tenant with a row, files in both folders and a secret is restored on an install with another keeper, and its secret opens there and not under the first keeper; other tenants are left alone; a tenant that is there is refused, and replaced when told to, with changes made after the backup gone; a wrong passphrase, a changed byte and a cut file write nothing and never reach the keeper; a failing `Ready`, alone and on a replace, leaves the data folder as it was; hand-made backups with a migration this binary does not know, a newer version, another tenant's database, no name, no database, secrets it does not hold or that do not open, a file outside the tenant's folder and a database journal are refused; a backup taken during another handle's open write holds what was committed.
- `go test ./tools/tenant`, the item's proof: a tenant with a password, a session, two chats, a file and a secret is backed up through the command, restored into a fresh data folder with another memory keeper, signs in with its password while its old session is refused, its chats list, read, search and hold their file, and its secret comes back. Nothing printed holds the key or the passphrase.
- `verify`, `no keys in output`, the surface `backup and restore` (`tools/verify/backup.go`): the stored value is looked for in the backup file, in everything a backup and a restore return or print, and in both data folders; through the package with two memory keepers, and through the built command with two key files, where the restored tenant's secrets list under the second key file and are refused under the first.

## Not here yet

- Scheduled backups (decision 0263).
- Restoring a tenant under a new id, beside the one it was made from.
- A screen or a route. Backups are made at the command line.
- The backup of a whole install at once. Each tenant is backed up by itself.
