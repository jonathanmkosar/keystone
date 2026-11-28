# 0650. An admin is a separate account in its own database, data/admin/admin.db, made from the command line alone

November 27, 2026 · decided in P15-01 · replaces decisions [0640](0640-an-admin-is-a-person-with-a-mark-set-from-the-command-line.md) and [0643](0643-step-up-proves-the-password-again-on-the-same-session.md)

## The question

The first pass of P15-01 made an admin a person with a mark in `keystone.json`, with a step-up before the admin area (decisions 0640 and 0643), on the spec's assumed answer to A1. On November 27, 2026 the owner answered A1 the other way: an admin is a separate account, and signing in as one shows only admin controls. Where does that account live, how does it sign in, and how is the first one made?

## The decision

- **Its own database.** Admin accounts are kept apart from people, in `data/admin/admin.db`, owned by a new package, `internal/adminaccount`, with migrations of its own: the accounts (an id of 16 hex characters, a name unique in any case, when added, the last sign-in), one password each, passkeys, passkey prompts, sessions and the admin audit log. An admin account has no tenant and no tenant database, so it has nothing of a person's to show.
- **The same rules as a person's sign-in.** The password is hashed, checked, counted and waited on under the `signin.` settings, through the same code: `internal/signin` exports the pieces (`CheckPasswordRules`, `HashPassword`, `PasswordMatches`, and the passkey readers and `VerifyPasskey`) in a new file, `shared.go`, and the count of wrong tries is one statement on the account's own row, as a person's is. An account that is not there takes as long to refuse as a wrong password. Passkeys stay behind `signin.passkeys`, off as shipped.
- **Sign-in by name or id.** `POST /api/v1/admin/session` takes the account's name or id with the password, or a passkey's answer. A person's door does not open an admin account, and the admin's door does not open a person.
- **The command line alone makes and removes an account.** `go run ./tools/admin add "Name"`, then `go run ./tools/admin password <id>` (from standard input). `list` shows the accounts, and `remove <id>` refuses the last one without `--yes`. No route makes or removes one, and the `admin isolation` check fails if any code but `internal/adminaccount` and `tools/admin` calls `AddAccount` or `RemoveAccount`.
- **The owner has two sign-ins**: his own person, and his admin account.
- **Not encrypted at rest.** The admin database holds account names, hashes, passkeys' public keys and the admin audit log, and no person's data. Encryption at rest (P15-06) is for people's data. If the owner wants the admin database sealed too, it can be added to P15-06 or after.

## Why

- The owner's answer: an admin account shows only admin controls, with no chats, files or models of its own. A database of its own makes that true by construction: there is no tenant behind it to show.
- The data folder already keeps people apart by folder. A folder of its own for the admin keeps the admin out of every tenant's folder and every tenant out of the admin's.
- One sign-in implementation, two stores: the rules a person's password meets were reviewed and tested once (decisions 0010 and the signin tests); copying them would let the two drift.
- The command line already owns the data folder, so making the first admin there gives no one a new power (as 0640 said, and that reason stands).

## What it costs

- The step-up is gone: the admin's session is an admin session from the start, and is short instead (decision 0652).
- Two databases to open for an admin read of a person: the admin's, to check the account, and the person's. Both are opened per call, as a person's is.
- An admin account with no password cannot sign in until `tools/admin password` is run; `list` says which have none.
