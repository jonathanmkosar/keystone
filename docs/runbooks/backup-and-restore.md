# Backing up a tenant and restoring it

A backup is one person's whole tenant in one file: chats, files, artifacts, settings, the audit log and their keys. It is sealed under a passphrase you choose. How it works is in [`docs/architecture/backup.md`](../architecture/backup.md).

Nothing makes a backup by itself. Make one when you want one: before an upgrade, before moving machines, and now and then.

## Choose a passphrase and keep it

The passphrase is the only way into the backup. It is not stored anywhere, and nothing can open the file without it.

- Use at least 12 characters (the `backup.passphrase_min_length` setting). Four or five random words are good.
- Keep it in your password manager, next to where the backup file lives.
- Whoever has the file and the passphrase has the person's keys and chats, on any machine. Keep the file somewhere you trust, such as an encrypted drive or a cloud folder only you can open.

## Make a backup

From the repo's folder. The tenant id comes from `go run ./tools/tenant list`.

1. Copy the passphrase from your password manager.
2. Pipe it in, and name a file that is not there yet. A date in the name keeps backups apart:

        Get-Clipboard | go run ./tools/tenant backup <tenant-id> D:\Backups\keystone-<tenant-id>-2026-11-25.backup

   Or type it when asked; it shows on the screen as you type, and you are asked twice.

3. It prints the tenant, the number of files and secrets, and that the file was read back and is whole. The secrets are counted, never named.

The server can keep running while a backup is made. A backup never replaces a file: give a new name each time. If anything fails, no file is left.

To check a backup later, without changing anything:

    Get-Clipboard | go run ./tools/tenant restore --check D:\Backups\keystone-<tenant-id>-2026-11-25.backup

## Restore on another machine

1. On the new machine, get Keystone running once (`go run ./tools/rebuild`), then **stop the server**. It keeps the list of people in memory and would write it back without the restored person.
2. Copy the backup file over.
3. Copy the passphrase, and restore:

        Get-Clipboard | go run ./tools/tenant restore D:\Backups\keystone-<tenant-id>-2026-11-25.backup

   `--data <folder>` before `restore` restores into another data folder. On a machine with no key store, choose the key file as for `secret set` ([secrets runbook](secrets.md)):

        Get-Clipboard | go run ./tools/tenant --keeper file restore <file>

4. It prints the tenant and the number of secrets now under this machine's master key. Start the server and sign in as that person, with the password they had when the backup was made. Every session from before is ended.

The restored person has the same id. Their keys work at once: the backup's data key is wrapped again under this machine's key store (DPAPI on Windows), and `go run ./tools/tenant secret list <tenant-id>` lists them. Passkeys work only if Keystone is reached by the same host name as before; the password always does.

## Restore over a tenant that is there

A restore refuses a person who is already on the install, and says so. To go back to the backup, which loses everything that person did since it was made:

1. Stop the server. On Windows the restore cannot move the person's folder while the server has it open.
2. Make a backup of the person as they are now, in case.
3. Restore with `--replace`:

        Get-Clipboard | go run ./tools/tenant restore --replace D:\Backups\keystone-<tenant-id>-2026-11-25.backup

If anything fails, the person is left as they were.

## The backups made before encryption

The first time the server starts with encryption at rest on (`encryption.at_rest`, on by default), it encrypts each person who is still plain, and writes a backup of each just before, to `data/backups/before-encryption/` (the `encryption.backup_folder` setting). Each backup has a `.key` file beside it holding its passphrase, wrapped by this install's master key: only this install, on this machine, under this Windows account, can read it.

To bring a person back as they were before they were encrypted, with the server stopped:

```
go run ./tools/tenant encryption passphrase data/backups/before-encryption/<file>.keystone-backup | go run ./tools/tenant restore --replace data/backups/before-encryption/<file>.keystone-backup
```

They come back plain. With `encryption.at_rest` still on, the next start encrypts them again, with a new backup; switch it off first to keep them plain.

These backups hold everything the person had. Once the encrypted install has worked for a while, a week of use, delete the folder. `go run ./tools/tenant encryption status` shows who is encrypted.

## When it says no

| Message | What to do |
| --- | --- |
| the passphrase does not open this backup | Check the passphrase, and that it was copied whole. A space at either end counts. |
| the backup is damaged | The file changed after it was written, or was cut short in a copy. Copy it again, or use an older backup. |
| made by a newer Keystone / has migration … which this binary does not know | Update this checkout, then restore again. |
| is already on this install | See "Restore over a tenant that is there". |
| its folder could not be moved aside | Stop the server and restore again. |
| secrets.bin was refused (during a backup) | This install cannot open the person's keys, for example after a change of Windows account. Follow "After a restore, or on another machine" in the [secrets runbook](secrets.md), store the keys again, and back up then. |

A backup or a restore that was killed partway can leave a folder whose name starts with `.backup-` in the data folder, or with `.<tenant-id>.restore-` in `data/tenants/`. Nothing reads them, and they can be removed once nothing is running.
