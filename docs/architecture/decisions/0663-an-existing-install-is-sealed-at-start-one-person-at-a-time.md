# 0663. An existing install is sealed at the server's start, one person at a time, after a backup, by a copy that trades places with the plain folder

November 27, 2026 · decided in P15-06

## The question

The owner's live install holds his chats in plain folders, and will be the first to go through this. The spec asks that an existing install be encrypted in place at the first start of the new version, one person at a time, after a backup of each, and that a person whose folder is half done not be started. How is that made safe against a stop at any moment, and against a mistake in the sealing itself?

## The decision

`Install.SealAll`, which the server calls before it serves anyone when `encryption.at_rest` is on. For each plain person, in the registry's order:

1. **A backup in the P13-04 format**, to `encryption.backup_folder` (`backups/before-encryption` in the data folder). There is nobody at a start to type a passphrase, so Keystone makes a random one and keeps it beside the backup in a `.key` file, wrapped by the install master key and bound to the tenant id. The backup is read back whole with it before anything else happens. `go run ./tools/tenant encryption passphrase <file>` prints the passphrase, so `tenant restore` takes the backup as it takes any other.
2. **A sealed copy, beside the folder**, in `tenants/.<id>.sealing/`: the database copied by SQLite with `VACUUM INTO` through the VFS, the secrets file as it is, and every file sealed under its keyed name. Anything else at the top of the folder stops the sealing: nothing is left behind unsealed or dropped without a word.
3. **Read back before anything trades places**: SQLite's `integrity_check` on the copy, every table's rows counted and hashed with their rowids on both sides, every file opened again and compared by SHA-256, everything synced, and `seal.json` written last, so a copy with `seal.json` is a whole one.
4. **Two renames**: the plain folder to `tenants/.<id>.plain/`, the copy into place. The sealed folder is opened there and checked by SQLite; if that fails, the plain folder goes back.
5. **Only then** is the plain folder removed. No plain file is removed before its sealed copy was written, synced and read back.

A failure leaves that person plain and as they were, stops the run, and is written in the server's log by tenant id; the server still starts, and tries again at its next start.

**After a stop**, `Install.recover`, which `tenant.Open` runs for every person whatever the setting says, decides from what it finds: a copy without `seal.json` is removed; with the plain folder aside and a whole copy, the copy goes in place; with the plain folder aside and no whole copy, the plain folder goes back; with the sealed folder in place and the plain one aside, the sealed one is opened and checked before the plain one is removed. A folder whose database and `seal.json` disagree is refused, never opened.

A new person is sealed when they are made (`Install.Create`), with nothing of theirs in the folder yet.

## Why

- The folder in place is only ever the whole plain one or the whole sealed one, so there is no half-done person to refuse in the normal course; the refusal is for a folder changed by hand.
- Copying beside and trading places with renames means the plain data is never changed: a mistake in the sealing is found by the read-back while the plain folder is still in place, and a stop at any moment leaves something whole.
- The backup is the second line, for a mistake the read-back does not see. A random passphrase wrapped by the master key keeps it sealed (decision 0260) without asking anyone anything at a start, and only this machine's account can read it, as only it can read the person's data.
- Stopping at the first failure keeps one problem from becoming many, and starting the server anyway keeps the owner working while it is looked at.

## What it costs

- The first start takes longer: a backup with its passphrase rounds (`backup.passphrase_rounds`, about a second), and a full copy, check and hash of each person.
- The backups stay until the owner deletes them; they hold everything the person had, sealed under a passphrase only this install can unwrap. The runbook and the setting's description say to delete them once the encrypted install has worked for a while.
- Room on the disk for a second copy of the largest person, and their backup, while they are sealed.
- A command run against the plain database during the first start, between the copy and the rename, could write something the sealed copy does not have. On Windows the rename fails while a command holds a file, and the person is left plain; do not run commands while the server's first start is sealing.
- A person whose folder holds something Keystone does not keep there stays plain until it is moved out by hand.
