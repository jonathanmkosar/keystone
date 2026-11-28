# 0640. An admin is a person with a mark in keystone.json, set from the command line alone

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A1

**Superseded** on November 27, 2026 by decision [0650](0650-an-admin-is-a-separate-account-in-its-own-database.md): the owner answered A1, an admin is a separate account in its own database, not a person with a mark. Kept for the record.

## The question

Keystone has no admin. The admin and views brief asks for one who can see the whole install and set what is theirs to set. Is an admin a second kind of account with its own sign-in, or a person who is marked as one? And how does the first one come to be?

## The decision

- **A mark on a person.** Each tenant's entry in `keystone.json` holds `admin`, `true` or `false`. An admin signs in as themselves, with their own password or passkey, and keeps their own chats and settings like anyone else. There is no second account and no shared table of people.
- **The command line alone changes it.** `go run ./tools/tenant admin <tenant-id> on|off` calls `tenant.Install.SetAdmin`, and nothing else does. No route can: the `admin isolation` check fails if any code outside `internal/tenant` and `tools/tenant` calls `SetAdmin`.
- **The last admin is kept unless asked twice.** Taking the mark from the last admin is refused with a warning, and done only with `--yes`.
- **The mark is not enough.** The admin area also asks for the password or a passkey again (decision 0643).

This is the spec's assumed answer A1. Everything resting on it is provisional until the owner answers.

## Why

- One sign-in per person is what a tenant already is (`tenants.md`, `signin.md`). A second account would need a second sign-in, a second session store and a way to tie the two together, all to say "this person may also see the install".
- Whoever can run the command line already owns the data folder. Making the first admin there gives no one a power they did not have; a browser path to becoming an admin would be a new way in.
- A mark in the registry is install data, beside the name and the id, and is not in the person's own database, which they could restore from a backup of their own (decision 0641).

## What it costs

- An admin's ordinary session and their admin step-up are the same session. Someone who finds an admin's unlocked browser still needs the password again to reach the admin area.
- Removing the last admin leaves an install with none until the command line marks another. `--yes` is the way to say that is meant.
- If the owner answers that an admin is a separate account, the mark becomes that account's flag, and the routes and the door keep their shape.
