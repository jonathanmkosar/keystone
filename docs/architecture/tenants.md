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

`data/admin/` is the install's own folder, beside `tenants/`, for what belongs to no one person: the install's search providers in `search.db` and the install vault `secrets.bin` (P7-06, [web-search.md](web-search.md)), and the admin accounts' `admin.db` (P15-01). `Install.AdminFolder()` gives its path and makes nothing; a part makes the folder when it first writes there. `Install.InstallVault()` gives the install vault, opened with the install's keeper.

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

## The admin mark

Since P15-01 (provisional on the owner's answer A1), each entry in `keystone.json` holds `admin`, `true` or `false` ([admin.md](admin.md), decisions [0640](decisions/0640-an-admin-is-a-person-with-a-mark-set-from-the-command-line.md) and [0641](decisions/0641-the-mark-is-read-from-disk-at-each-admin-call.md)).

- `SetAdmin(id, on)` is the only code that changes it, and `go run ./tools/tenant admin` the only code that calls it. The `admin isolation` check holds that.
- `IsAdmin(id)` and `Marks()` read the file on disk each time, so a running server sees a change the command line made at once.
- Every save takes the marks from the file first (`save` reads them, then `write` writes), so a server that read the file before a change never writes the old mark back.
- `Tenants()` is still the list as it was read at start, and its `Admin` field may be old.
- A backup's manifest carries `admin` false, and a restore keeps the install's own mark for that id, none for a new one.

## The command

`go run ./tools/tenant add "Name"` adds a tenant and prints its id. `go run ./tools/tenant list` lists them, with the admin mark since P15-01. `go run ./tools/tenant admin <tenant-id> on|off [--yes]` gives or takes the mark. See [tools.md](tools.md).
