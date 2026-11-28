# Encryption at rest

Folders: `internal/atrest` (the keys, the sealed files and the database's VFS), `internal/tenant/atrest.go` and `folder.go` (what is sealed, when, and the first start), `internal/secrets/subkey.go` (the one key the person's data key gives out), `cmd/keystone/atrest.go` (the server's start) and `tools/tenant/encryption.go` (the command). Built in P15-06, from the spec's "Encryption at rest" (`docs/plan/SPEC-router-and-admin.md`, Part 2). Decisions [0660](decisions/0660-the-database-is-sealed-by-a-vfs-under-the-same-driver.md) (the database), [0661](decisions/0661-files-are-sealed-whole-in-chunks-under-a-key-of-their-own.md) (files), [0662](decisions/0662-names-on-disk-are-keyed-hashes.md) (names), [0663](decisions/0663-an-existing-install-is-sealed-at-start-one-person-at-a-time.md) (the first start) and [0664](decisions/0664-a-sealed-persons-backup-carries-the-sealed-bytes.md) (backup and restore).

Every person's data is encrypted on disk with their own key, held by the operating system: their database with its journal, their uploads, project files and artifacts. A sealed person's folder holds nothing in plain text: no word they wrote, no SQLite header, and no name of a file or folder that came from them.

## What it protects, and what it does not

It protects a copied data folder, a stolen disk, and a backup read without its passphrase. It does not protect against someone who controls the machine and runs code as the account Keystone runs as: that account can ask the operating system for the master key, as Keystone does. The owner chose this, knowing it (the spec's owner's answers, November 27, 2026).

## The keys

```
install master key (the OS: DPAPI, keychain, Secret Service; decision 0006)
  └─ wraps the person's data key, in secrets.bin, bound to the tenant id
       └─ Vault.Subkey("keystone at rest v1"): HKDF-SHA-256, bound to the tenant id
            ├─ pages key  → one key per database or journal file (its salt) → one key per block written
            ├─ files key  → one key per sealed file (its salt)
            └─ names key  → keyed names on disk, and the folder's check
```

The data key never leaves `internal/secrets` (decision 0261): `Vault.Subkey` gives a key derived from it for one named purpose, and makes the data key, and an empty `secrets.bin`, for a person who has none. `atrest.Derive` makes the three keys from that subkey. `tenant.Install` keeps each person's keys in memory once they are unwrapped, so the operating system is asked once per person per process.

## A sealed folder

| File | What it is |
| --- | --- |
| `seal.json` | Says the folder is sealed: format, version, tenant id, when, and a check (a keyed hash) that tells whether the keys at hand are this folder's. It holds no key. It is written last when a folder is sealed. |
| `keystone.db` | The database, as a page file (below). |
| `keystone.db-journal` | Its rollback journal, as a page file. A header only, between transactions. |
| `secrets.bin` | As it always was: sealed with the data key (decision 0006). |
| `files/<keyed name>` | Each upload and project file, sealed. |
| `artifacts/<keyed name>/<keyed name>/<keyed name>` | Each artifact version, sealed, under the chat's, the artifact's and the version's keyed names. |

A folder without `seal.json` is plain and opens as it always did. A folder whose database and `seal.json` disagree (a sealed database with no `seal.json`, or a plain one with it) is refused as half sealed and left as it is.

## The database

`internal/atrest` registers a VFS with the driver Keystone already uses (`modernc.org/sqlite/vfs.Register`, decision 0660). It opens only `keystone.db` and `keystone.db-journal`, and only in a folder whose keys `atrest.Use` was given; any other name is refused. SQLite reads and writes through it as through a plain file; on disk each file is a page file:

| Part | Size | What it is |
| --- | --- | --- |
| magic | 8 bytes | `KSPAGE01`. |
| block size | 4 bytes | 4096, SQLite's page size and the sector size the driver tells SQLite. |
| salt | 16 bytes | Random, new for each file. The file's key is HKDF of the pages key and the salt. |
| check | 32 bytes | A keyed hash of the file's key, so another person's file, or another master key, is refused at open. |
| | 4 bytes | Zero. |
| slots | the rest | One for each block of 4096 bytes: a 24-byte random nonce, the length, the block sealed with AES-256-GCM, and its tag. |

Each slot is sealed under a key of its own, derived from the file's key and the first half of its nonce, so no key sees enough nonces for two to meet however often a page is written. The tenant id, the slot's number and its length are its additional data, so a slot moved within a file, or to another person's file, does not open. A slot shorter than a block holds the start of it, and the rest reads as zeros; only the last slot gives the file its size, and a write past the end never rewrites the slot before it, so a write cut short damages only the blocks it was writing.

A slot that does not open is an error in the database. In the journal it reads as zeros: the journal is written and synced before the database is touched and read back only after a crash, when SQLite takes a torn sector for garbage and its own checksums find the end of what was written whole.

- **Temporary files** never reach the disk: the driver keeps them in memory.
- **The journal**, not the write-ahead log. The driver's VFS has no shared memory, and the write-ahead log needs it unless one connection holds the database alone. A sealed database uses `journal_mode=truncate` and `synchronous=full`; the plain one keeps the write-ahead log and `synchronous=normal`.
- **One process at a time.** The driver keeps the connections of one process apart, and the VFS keeps one handle on each file for the whole process, owned by a hold that the first connection takes and the last lets go. The hold locks the file (`LockFileEx` on a byte far past its end on Windows, `flock` on Unix), so a second process, the server and a command, waits up to `encryption.lock_wait` and is then refused with `atrest.ErrBusy`. A plain database still has SQLite's own locks.
- **Its cost**, from the load test's small profile on Windows 11 (P13-03): streaming a reply, the first words to the last, about 490 ms against 16 ms plain, inside its budget of 1 s; context packing the long chat about 90 ms against 42 ms; everything else inside its budget with room. The cost is the syncs a rollback journal needs at each commit, which the write-ahead log does not, and decrypting pages after each new connection.

## Files

`Store.FilesFolder()` and `Store.ArtifactsFolder()` return a `tenant.Folder`, with `os.Root`'s methods (`WriteFile`, `ReadFile`, `Stat`, `Remove`, `RemoveAll`, `Rename`, `MkdirAll`), so the code that keeps files changes `s.Files()` to `s.FilesFolder()` and nothing else. For a sealed person every file is sealed on the way in and opened on the way out, and every part of a path is a keyed hash on disk; for a plain person it is the folder as it is. `Stat` describes the file on disk, which is larger than what it holds. The raw `Files()` and `Artifacts()` stay, for the size of a folder (Operations), the drills and the browser's profile.

A sealed file is the magic `KSFILE01`, the chunk size (64 KiB), a random salt, then the chunks, each AES-256-GCM under the file's own key with its number and a last-chunk flag for its nonce and the header and the tenant id for additional data (decision 0661). A file cut at a chunk's edge, a changed bit, or another person's file is refused with `atrest.ErrSealed`.

Names (decision 0662): `Keys.Name` is the first 16 bytes of HMAC-SHA-256 under the names key, as hex. An upload kept by its SHA-256 has a different name on disk for every person, and a chat's or an artifact's id does not show.

The code that writes those files is the chats lane's `internal/uploads` and `internal/chat` (removing a chat's files), the projects lane's `internal/projects/blobs.go`, and the agents lane's `internal/artifacts`; each changed only at the lines that write, read, stat, rename and remove them.

## The first start

With `encryption.at_rest` on, the server calls `Install.SealAll` before it serves anyone (decision 0663). For each person who is still plain, one at a time:

1. **A backup**, in the P13-04 format, to `encryption.backup_folder` (`backups/before-encryption` in the data folder), under a random passphrase. The passphrase is wrapped by the install master key, bound to the tenant id, in a file of the same name and `.key`. The backup is read back whole with the passphrase before anything else happens. `go run ./tools/tenant encryption passphrase <file>` prints the passphrase, for `tenant restore`.
2. **A sealed copy** in `tenants/.<id>.sealing/`: the person's keys (which make their data key if they had none), the database copied by SQLite (`VACUUM INTO` through the VFS), `secrets.bin` copied as it is, and every file under `files` and `artifacts` sealed under its keyed name. Anything else at the top of the folder is refused, so nothing is left behind unsealed or dropped without a word; the agent's browser profile, made new at each start, is left out.
3. **Read back**: the copy opened through the VFS, `PRAGMA integrity_check`, and every table's rows counted and hashed, rowid and all, on both sides; every file opened again and compared by SHA-256; everything synced; `seal.json` written last.
4. **Trade places**: the plain folder renamed to `tenants/.<id>.plain/`, the sealed one into place, and opened there with SQLite's check. If it does not open, the plain one is put back.
5. **Only then** the plain copy removed.

A failure leaves that person plain and as they were, stops the run, and is written in the server's log; the server still starts and tries again at its next start. Every line names a person by id only.

**A stop at any moment** is finished or undone the next time the install opens (`Install.recover`, run for every person by `tenant.Open` whatever the setting says): a sealing folder without `seal.json` is removed; with the plain folder moved aside and a whole sealed copy, the copy goes in place; with the plain folder moved aside and no whole copy, the plain folder goes back; a sealed folder in place with the plain copy beside it opens and is checked before the copy is removed. The folder in place is only ever the whole plain one or the whole sealed one.

**A new person** is made sealed: `Install.Create` makes the folder and seals it at once (nothing of theirs is in it yet). A person added from the command line, which does not read the setting, is sealed at the server's next start.

## Backup and restore

A sealed person's backup carries their sealed database (the snapshot is made through the VFS under the same keys), `seal.json` and their sealed files, with the data key re-wrapped under the passphrase as before (decisions 0261, 0664): still sealed under its passphrase (decision 0260). A restore opens it with the keys from its own secrets file, now under this install's keeper, and the person comes back sealed, on this machine or another. A plain backup restores plain and is sealed at the next start.

## Settings

| Key | Type | Default | Scope |
| --- | --- | --- | --- |
| `encryption.at_rest` | bool | true | install |
| `encryption.lock_wait` | duration | 30s | install |
| `encryption.backup_folder` | string | backups/before-encryption | install |

Switching `encryption.at_rest` off stops new people being sealed and stops the first start; a sealed person stays sealed and opens as before. Nothing turns a sealed person back to plain.

## The command

```
go run ./tools/tenant encryption status
go run ./tools/tenant encryption passphrase <backup file> | go run ./tools/tenant restore --replace <backup file>
```

## Checked by

- `internal/atrest` tests: sealed files of every size round the chunk, refused with another person's keys, a changed bit or a cut at a chunk's edge; a page file written, read and truncated at random against a plain buffer; a torn block an error in the database and zeros in the journal; a database through the VFS from three connections at once, nothing plain on disk, its journal on disk during a transaction, and refused under other keys; the VFS refusing other files; the lock keeping a second process out and letting go at the last close.
- `internal/tenant` tests: a new person sealed, every file read from disk without the marker word, a plain header or a plain name; the install refused under another master key; the folder copied into another person's (whole, or the database and files alone), and one file alone, refused; a half-sealed folder refused; a plain install of two sealed in place with a backup each that opens with its passphrase, and not under another master key; the data folder taken at each step of a sealing, as a stop would leave it, opening whole with every word and sealing again; a sealed backup restored sealed on another install.
- `cmd/keystone`: the server seals a plain person at start, says so in its log by id, and the client then signs that person in.
- The standing check **encryption at rest** (`tools/verify/encryption.go`): the real packages (a chat and its message, an upload, an artifact, a project file, a knowledge page) on a sealed person, the scan of the folder, another master key, the folder in another person's, and a plain install sealed as the first start seals it.
- The load test runs with encryption on, as it ships.

## Not built yet

- Rotating a person's data key. The keys are the same for as long as the data key is (decision 0261).
- Sealing the agent's browser profile while its browser runs. It is made new at each start and removed at close (P10-03).
- Turning a sealed person back to plain.
- A slot rolled back to an older version of itself, by someone who can write the folder, is not told apart: each slot is bound to its place, not to a version. It gives nothing away, and SQLite's own checks find most such changes.
- The physical slots are 4140 bytes and not aligned to the disk's sectors, so a block's write touches the sectors of its neighbours. Like SQLite's own default on Unix, this relies on the disk not damaging bytes outside a write when power is lost.
