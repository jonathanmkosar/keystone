# 0664. A sealed person's backup carries their sealed bytes, and restores sealed

November 27, 2026 · decided in P15-06

## The question

A backup (P13-04) holds a person's database, copied by SQLite, their secrets file with the data key re-wrapped under the backup's passphrase (decision 0261), and every other file. With encryption at rest, should a backup hold the person's data plain inside the passphrase's seal, or as it is on disk?

## The decision

**As it is on disk.** For a sealed person, `Install.Backup` gives the VFS the person's keys for its working folder, so `VACUUM INTO` writes the snapshot sealed under the same keys, and the backup carries it, `seal.json` and the sealed files as they are. A restore opens the restored folder with the keys from its own secrets file, re-wrapped under this install's keeper, before it is put in place, and the person comes back sealed, on this machine or another. A backup of a plain person stays plain inside its seal, restores plain, and is sealed at the next start.

## Why

- Nothing of a person's is ever plain on disk, not even in the backup's working folder for a moment.
- The data key already travels in the backup, re-wrapped (decision 0261), and the sealed bytes are bound to the tenant id, which a restore keeps: the sealed bytes open after a restore with nothing else to carry.
- The backup's own format, checks and commands do not change.

## What it costs

- A backup of a sealed person compresses less: sealed bytes do not compress.
- A sealed backup restored by an older Keystone, without the VFS, does not open; it is refused when the restore opens the database, before anything is put in place.
- A restore that replaces a person replaces their data key with the backup's; the install forgets the keys it kept for that person.
