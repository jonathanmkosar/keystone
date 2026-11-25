# 0262. A restore checks the whole file first, builds the tenant beside the others, and ends its old sessions

November 25, 2026 · decided in P13-04

## The question

A restore writes a tenant's database, files and secrets into a data folder that may hold other people, and may replace a tenant that is there. The item asks that it refuse to overwrite an existing tenant unless told to, check the file's integrity before writing anything, and leave nothing half-written. How?

## The decision

**Two readings of the file.** The first reads it all and writes nothing: every chunk, the manifest, every name, the database's migrations against the ones this binary knows, and the secrets, opened with the backup's own keeper. Only after that is the new install's keeper asked to wrap the data key. The second reading extracts, and must see the same entries and the same SHA-256 of the whole file, or it stops. The key made from the passphrase is kept between the two, so the passphrase goes through its rounds once.

**The tenant is built in a folder beside the others** (`tenants/.<id>.restore-…`), inside the data folder so a rename can move it. There the database is opened, brought up to date by the migrations, checked by SQLite's `quick_check` and checked to be this tenant's; `restored_at` and `restored_from_backup_made_at` are written to `tenant_meta`; the secrets must open under the new keeper; and the caller's `Ready` runs on the store. Any failure removes the folder, and the registry never heard of it.

**Then one rename and one save.** The folder is renamed to `tenants/<id>` and the tenant is added to `keystone.json`. If the save fails, the folder is taken back out.

**A tenant that is there is refused** unless the restore is told to replace it (`restore --replace`), by its entry in the registry or by a folder with its id. The check runs as soon as the manifest is read, and again under the install's lock before the rename. A replace moves the old folder aside, renames the new one in, saves, and only then removes the old one; if anything fails before the save, the old folder is put back.

**The restored tenant starts signed out.** The command passes `signin.SignOutAll` as `Ready`, so every session the backup held is ended before the tenant can be reached. The tenant signs in with the password it had when the backup was made.

**A backup runs beside the server; a restore does not.** The database is copied with SQLite's `VACUUM INTO`, one consistent snapshot. A restore changes the registry, which the server keeps in memory and writes back whole, so the runbook says to stop the server first.

## Why

- Checking first is the only way to write nothing on a bad file, and the file can be read twice because a restore takes a file, not a stream.
- A rename inside one folder is the one step a file system does whole. Everything that can fail happens before it.
- `internal/tenant` cannot import `internal/signin`, which imports it. `Ready` lets the command add steps without the tenant package knowing about sessions, and a step that fails still leaves nothing.
- A session token in a backup was given out on another machine. Ending them all costs one sign-in.

## What it costs

- A restore reads the file twice and needs room for the tenant in the data folder while it works.
- On Windows a replace fails while the server holds the tenant's database open: the folder cannot be moved aside. The message says to stop the server. On macOS and Linux the move would succeed under a running server, which would go on writing to the old files; the runbook is the guard there.
- If the process is killed during a restore, a folder named `.<id>.restore-…` or `.<id>.restore-…-replaced` can be left in `tenants/`, and a `.backup-…` folder in the data folder after a killed backup. Nothing reads them. The runbook says they can be removed. A killed replace between its rename and its save leaves the new folder in place with the old registry entry, which is the same tenant.
- Passkeys are bound to the host name they were made for (`signin.passkey_rp_id`). Restored on a host with another name, they do not work, and the password does.
