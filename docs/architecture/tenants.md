# Tenants

Folder: `internal/tenant`

A tenant is one person. This part holds the install's list of tenants and the handle that reaches one tenant's data. Decision [0003](decisions/0003-sqlite-per-tenant.md) says why each tenant has its own database.

## The data folder

```
data/
  keystone.json          the tenant registry, and later the install's other settings
  tenants/<tenant-id>/
    keystone.db          the tenant's database
    files/               uploads and attachments
    artifacts/           artifact versions
```

The data folder is the `data.dir` setting. A relative folder is read from the repo root. `keystone.json` is written to a temporary file and renamed into place, so a crash never leaves half a registry. Fields this part does not know are kept when it saves.

## Tenant ids

An id is sixteen random hex characters. A handle is opened by id, and only for an id in the registry. Anything else, such as `..` or a path, is refused before any folder is touched.

## The store handle

`Install.Store(id)` returns a `Store` for one tenant. It holds that tenant's database and its `files/` and `artifacts/` folders as `os.Root` values, which refuse any path that leaves the folder. A handle has no way to name another tenant.

Each database records its tenant id in `tenant_meta`. Opening a database whose id is not the folder's id is refused, so a file copied from one tenant's folder to another's cannot be read as the wrong person's.

The connection settings follow Waymarks: write-ahead log, a five-second wait when the file is busy, foreign keys on, one connection, and write transactions that take the lock at once.

## Migrations

Migrations are SQL files in `internal/tenant/migrations/`, named by date and time: `20261124T180000_tenant_core.sql`. They are applied in name order, each in its own transaction, and recorded by name in `schema_migrations`. They move forward only.

PARALLEL.md asks for date-and-time names so two lanes can add migrations at once. Because of that, a migration with an earlier name than one already applied still runs. A database that holds a migration this binary does not know is refused, with a message to use a newer binary.

A lane adds a table by adding a file. A shipped file is never edited.

## Backup and restore

`Install.Backup` writes one tenant to one file sealed under a passphrase, and `Install.Restore` brings it into a data folder, on this machine or another, with its keys under that install's keeper. A restore refuses a tenant that is already there unless told to replace it, checks the whole file before it writes anything, builds the tenant in a folder beside the others and renames it into place. `openStore` opens that folder as a store before it is in the registry. The code is in `internal/tenant/backup.go`, and the file's format in `internal/backup`. See [backup.md](backup.md).

## No admin mark

The first pass of P15-01 kept an admin mark on each tenant in `keystone.json`. Since the owner answered A1, an admin is a separate account in `data/admin/admin.db` ([admin.md](admin.md), decision [0650](decisions/0650-an-admin-is-a-separate-account-in-its-own-database.md)), and the mark is gone: `Tenant` has no `Admin` field, and `SetAdmin`, `IsAdmin` and `Marks` are removed. A `keystone.json` that still holds `"admin"` on a tenant loads, and the next save drops it (decision [0653](decisions/0653-the-mark-and-the-step-up-are-taken-back-by-a-forward-migration.md)). `Install.Dir()` returns the data folder, where the admin database lives beside `tenants/`.

## The command

`go run ./tools/tenant add "Name"` adds a tenant and prints its id. `go run ./tools/tenant list` lists them. Admin accounts are made with `go run ./tools/admin`. See [tools.md](tools.md).
