# 0660. A person's database is sealed page by page by a VFS of Keystone's own, under the same driver, with a rollback journal

November 27, 2026 · decided in P15-06

## The question

The owner chose encryption at rest with server-held keys: every person's database, with its journal and temporary files, encrypted with their own key. The store is `modernc.org/sqlite`, the driver Waymarks uses (decision 0003), and it has no encryption of its own. The store must stay pure Go, with no C compiler. How is the database encrypted?

## The options weighed

1. **A VFS of Keystone's own under the same driver.** `modernc.org/sqlite` v1.63.0, the version Keystone already has, lets Go code register a read-write VFS (`vfs.Register`). Every file SQLite opens by name goes through it; temporary files stay in memory and never reach the disk. Its limits: no shared memory, so the write-ahead log works only for one connection holding the database alone; and no lock between processes.
2. **Another pure-Go driver with an encrypting VFS**, such as `github.com/ncruces/go-sqlite3`, which runs SQLite compiled to WebAssembly under `wazero` and ships VFSs that encrypt (Adiantum, XTS). It keeps the write-ahead log and locks between processes. It changes the driver of every database in Keystone, adds a WebAssembly runtime and its modules, loses Waymarks parity, and its data source names, its time handling and its behaviour under load would all have to be learned again; it was not on the machine's list of modules Keystone has measured.
3. **Sealing the whole file**: the database opened plain in memory, or in a temporary file, and sealed whole on each change. Every write would rewrite the whole database, a crash between would lose or damage it, and a temporary plain file would be plain on disk.
4. **SQLCipher or SQLite's own SEE**: C, with a C compiler on every machine. Ruled out by decision 0003.

## The decision

**Option 1.** `internal/atrest` registers one VFS for the process. It opens only `keystone.db` and `keystone.db-journal`, only in a folder whose keys `atrest.Use` was given, and seals each in blocks of 4096 bytes (SQLite's page and the sector size the driver tells SQLite) with AES-256-GCM: a slot per block, a random 24-byte nonce whose first half picks the block's key, the tenant id, the block's place and its length as additional data (`docs/architecture/encryption.md` has the format).

- **A rollback journal, not the write-ahead log.** A sealed database uses `journal_mode=truncate` and `synchronous=full`. The write-ahead log without shared memory needs one connection to hold the database alone, and Keystone opens a store per request, from many goroutines; making every store of a person share one connection would change how every lane's code may use the database.
- **A torn block** is an error in the database and zeros in the journal, which is what SQLite expects of a torn sector there: the journal is synced before the database is touched, and SQLite's own checksums find where it stops.
- **One process at a time.** The VFS keeps one handle on each file for the whole process, owned by a hold the first connection takes and the last lets go, and locks the file with it (`LockFileEx` on a byte far past the end on Windows, `flock` on Unix). Another process waits up to `encryption.lock_wait` (30s) and is then refused. Sharing the handle is also what made it fast: Windows takes about a millisecond to open a file, and the driver closes the journal after every transaction.

## Why

- No new module, no cgo, and the same driver as Waymarks: decision 0003 stands, and every query, migration and pragma Keystone has keeps working.
- The VFS is the one place every byte of the database and its journal passes through, so nothing is missed: the audit log, vectors, snapshots and every table to come are sealed without anyone thinking of them.
- Measured with the load test's small profile on Windows 11 (P13-03): streaming a reply, the first words to the last, about 490 ms against 16 ms plain, inside its budget of 1 s; context packing the long chat about 90 ms against 42 ms; every other measure inside its budget. The first version, which opened the journal at each transaction, took 0.9 s to 3.3 s for the same stream.

## What it costs

- **Waymarks parity, in part.** The driver is the same; the journal is not. Waymarks keeps the write-ahead log, and a sealed Keystone database does not. A plain database still does.
- **Writes are slower.** Each commit syncs the journal and the database, which the write-ahead log with `synchronous=normal` does not. A person's chats are far below where that matters; the load test holds it to its budgets.
- **One process at a time** has a person's database open. A command run while the server has that person open waits, up to 30 seconds, and is then refused; the server waits for a command the same way. Before, SQLite's own locks let both work at once. A command that changes a person (`tenant secret set`, `tenant backup`) is short, and the server opens a person per request.
- **Backup and restore** (P13-04) keep working: `VACUUM INTO` writes the snapshot through the VFS, sealed under the same keys, and the backup carries it as it is (decision 0664). A backup made by an older Keystone, with a plain database, still restores.
- **The sectors.** A slot is 4140 bytes, so slots are not aligned to the disk's sectors, and a block's write touches the sectors of its neighbours. Like SQLite's own default on Unix (powersafe overwrite), this relies on the disk not damaging bytes outside a write when power is lost.
- The VFS relies on the driver's `vfs` package, new in v1.63.0. A later driver must keep it, and the tests in `internal/atrest` run SQLite through it.
