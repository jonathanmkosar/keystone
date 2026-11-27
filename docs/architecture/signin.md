# Sign-in and sessions

Folder: `internal/signin`

How a person proves who they are, and the session that proof buys. Every function works on one tenant's store handle ([tenants.md](tenants.md)), and everything it keeps is in that tenant's own database. There is no shared table of sessions or of people.

A tenant is one person, so there are no user names and no table of users. A tenant has at most one password and any number of passkeys.

There are no routes yet. The API shell (P2-05) calls the functions listed under "For the API" below.

## What is stored

The migration `20261124T210000_sign_in.sql` adds four tables to each tenant's database.

| Table | Holds |
| --- | --- |
| `signin_password` | One row at most: the salt, the hash, how it was made, and a count of wrong tries. |
| `signin_sessions` | One row for each signed-in browser or device: a session id, the hash of its token, how it signed in, when it began and when it ends. |
| `signin_passkeys` | One row for each passkey: its credential id, its public key, its use count and the person's name for it. |
| `signin_challenges` | Passkey prompts that have been handed out and not yet answered. |

No password and no session token is ever stored. A test reads the database files from disk and looks for both.

## Passwords

`SetPassword` stores a password as a salted PBKDF2-SHA-256 hash, from the standard library's `crypto/pbkdf2`. The salt is sixteen random bytes, new each time. The row records the algorithm and the number of rounds, so both can change later without losing anyone's password. When the rounds setting changes, a stored hash moves to the new count the next time its person signs in.

A new password must have at least `signin.password_min_length` characters, be at most 1024 bytes, and be UTF-8 text. Nothing else is asked of it.

Setting a password ends every session the tenant has. A password is usually changed because someone else may know it.

`SignIn` checks a password and opens a session. A wrong password and a tenant with no password give the same answer, `ErrRefused`, with no reason.

After `signin.failures_before_wait` wrong passwords in a row, the next try is `ErrWait`, and the password is not looked at, until `signin.failure_wait` has passed since the last try that was. Tries made during the wait do not make it longer. After the wait, one wrong password starts it again. A good sign-in clears the count. Each try is counted in one statement before the password is checked, so tries made at the same moment cannot slip past the limit together. The count belongs to one tenant: one person's wrong passwords never make another wait. A passkey still signs in during the wait.

## Sessions

A session token is `kss_`, the tenant's id, an underscore, and 32 random bytes as 64 hex characters. `SignIn` and `PasskeySignIn` return it once. The database keeps its SHA-256 and never the token. A fast hash is enough here because the token is random, not chosen by a person.

The token names its tenant so the server knows which database to look in. Decision [0007](decisions/0007-a-session-token-names-its-tenant.md) says why. The id in the token is a claim and proves nothing: a token is a session only when its hash is found in that tenant's database.

`Check(store, token)` is the test. It refuses, with `ErrNoSession`:

- a token that is not the right shape;
- a token that names any tenant but the store's, before the database is read;
- a token whose hash is not in the store's database;
- a session past its end, which is deleted on the spot.

So a session for tenant A is refused on tenant B's store. It stays refused when A's token is rewritten to name B, because the hash covers the whole token. It stays refused when A's session row is copied into B's database, because the token still names A.

`Resolve(install, token)` is the whole path for a route: read the tenant from the token, open that tenant's store, and `Check`. It returns the one store the session may use. Nothing else in a request chooses the tenant.

A `Session` carries an id of its own, for the audit log and for a list of signed-in devices. The id is not the token and opens nothing.

## Sign-out and expiry

`SignOut(store, token)` ends one session. `SignOutAll(store)` ends every session of that tenant. Neither can reach another tenant's sessions.

A session lasts `signin.session_lifetime` from sign-in, thirty days as shipped, and then the person signs in again. Using a session does not make it last longer. Sessions that have run out are deleted when one is presented and whenever someone signs in.

## Passkeys

The server side of WebAuthn, with the standard library alone. Decision [0012](decisions/0012-passkeys-without-a-cbor-reader.md) says how and what it costs.

Adding a passkey, from a signed-in session:

1. `BeginAddPasskey` returns a `PasskeyPrompt`: a random challenge, the site name (`signin.passkey_rp_id`), the tenant's id as the user id, and the passkeys the tenant already has.
2. The browser makes the passkey and the page sends back a `PasskeyCreation`: the credential id, `clientDataJSON`, `getAuthenticatorData()` and `getPublicKey()`.
3. `AddPasskey` checks it and stores the public key.

Signing in with one:

1. `BeginPasskeySignIn` returns a prompt with a fresh challenge.
2. The browser returns a `PasskeyAssertion`: the credential id, `clientDataJSON`, `authenticatorData`, `signature` and `userHandle`.
3. `PasskeySignIn` checks it and opens a session, as `SignIn` does.

What is checked, every time:

- The challenge is one this tenant's store handed out, for this purpose, in the last `signin.passkey_challenge_lifetime`, and has not come back before. It is deleted when it comes back, so an answer works once.
- The answer is the right kind (`webauthn.create` or `webauthn.get`), came from the page at `signin.passkey_origin`, and not from inside another site's page.
- The authenticator data is for this site, and says the person was present and was verified (a fingerprint, a face or a PIN). A passkey stands in for the password, so the device must check the person.
- When adding: the credential id matches the authenticator data, and the key is ES256, Ed25519, or RS256 of 2048 bits or more.
- When signing in: the passkey is in this tenant's database, the signature matches its key, and the user handle is this tenant's id.
- A device that counts its uses must count upward. A count that stands still or goes back is refused, because it means a copy. Passkeys that sync between devices always say zero, and that is accepted.

A passkey refusal is `ErrRefused` wrapping the reason. The reason is for the log, not for the person at the door.

Tenant A's passkey is not in tenant B's database, so it cannot sign in to B. A's passkey row copied into B's database is still refused, because the device says the passkey is A's.

Passkeys are behind `signin.passkeys`, which is off. The code is proven against an authenticator written in the tests, which builds its answers from the WebAuthn rules. No real browser has used it yet. The sign-in screen (P4-01) proves it in a browser and then the setting can go on.

A browser allows passkeys only on `localhost` or over HTTPS. `keystone serve` prints `http://127.0.0.1:7900`, which is not `localhost` to a browser: passkeys need `http://localhost:7900`. A phone on the home network reaches Keystone by a number, over plain HTTP, and cannot use a passkey until phone access is decided (P10-05).

## The admin step-up

Since P15-01 (provisional on the owner's answer A1), an admin's session can be stepped up: the password or a passkey proved again opens the admin area on that one session for `admin.step_up_for` ([admin.md](admin.md), decision [0643](decisions/0643-step-up-proves-the-password-again-on-the-same-session.md)).

- The migration `20261127T150000_admin_step_up.sql` adds `admin_until` to `signin_sessions`; `20261127T150100_step_up_challenges.sql` lets a prompt have the purpose `step-up`.
- `Session.AdminUntil` is read by `Check` with the rest of the session. `Session.SteppedUp(t)` says whether it is still fresh.
- `StepUp(store, sessionID, password, options, lasts)` checks the password through the same function as `SignIn` (`checkPassword`), so a wrong one counts towards the same wait and a try during the wait is `ErrWait` with the password unread. It sets `admin_until` to now plus `lasts`, never past the session's end. A session that is not one of the store's is `ErrNoSession`.
- `BeginPasskeyStepUp` and `PasskeyStepUp` do the same with a passkey, checked as a passkey sign-in is (`checkAssertion`). A sign-in prompt's answer cannot step up, and a step-up prompt's answer cannot sign in.
- `EndStepUp(store, sessionID)` closes it.

**Last sign-in.** Each time a session is opened, by a password or a passkey, `tenant_meta` keeps `last_sign_in`. `LastSignIn(store)` reads it, or the newest open session for a tenant who has not signed in since, for the admin's list of people (decision [0646](decisions/0646-the-first-admin-read-is-the-people-list-with-last-sign-in.md)).

## Settings

All in `settings/core.json`, all for the whole install.

| Key | Default | Meaning |
| --- | --- | --- |
| `signin.password_iterations` | 600000 | Rounds of PBKDF2-SHA-256. |
| `signin.password_min_length` | 12 | Fewest characters in a new password. |
| `signin.session_lifetime` | 720h | How long a sign-in lasts. |
| `signin.failures_before_wait` | 5 | Wrong passwords in a row before the wait. |
| `signin.failure_wait` | 1m | How long the wait is. |
| `signin.passkeys` | false | Whether passkeys may be added and used. |
| `signin.passkey_rp_id` | localhost | The host name passkeys are bound to. |
| `signin.passkey_origin` | http://localhost:7900 | The page a passkey answer must come from. |
| `signin.passkey_challenge_lifetime` | 5m | How long a passkey prompt can be answered. |

`Defaults` reads these from the registry. `ReadOptions` reads them from anywhere, for when stored values exist (P2-04). The four fixed rules are not among them: no setting makes a session reach another tenant.

## For the API

What P2-05 calls:

- On every route: `Resolve(install, token)`, then use only the store it returns. A server that keeps stores open uses `TenantOf(token)` to pick the store and `Check(store, token)` on it. `TenantOf` alone proves nothing.
- To sign in: `SignIn(store, password, options)`, or `BeginPasskeySignIn` and `PasskeySignIn`. The route must already know which tenant is signing in. How the sign-in screen asks is an open question for the owner; see the journal entry for this item.
- To sign out: `SignOut(store, token)`.
- From a signed-in session: `SetPassword`, `BeginAddPasskey`, `AddPasskey`, `Passkeys`, `RemovePasskey`, `SignOutAll`.
- `PasskeyAlgorithms()` is the list to offer a browser.

The token is a bearer token. Keeping it out of logs, the audit log and API responses, and the cookie's flags, belong to the API.

## The command

`go run ./tools/tenant password <tenant-id>` sets a person's password before any sign-in screen exists. It reads the password from standard input. See [tools.md](tools.md) and the runbook [adding a tenant](../runbooks/adding-a-tenant.md).

## Checked by

- `go test ./internal/signin`. A session for one tenant is refused for another: on the other's store, re-addressed, and with its row copied across. The same for a passkey. Wrong and malformed tokens, sign-out, expiry, the wait after wrong passwords, and a passkey answer changed in each of twenty-one ways are each refused.
- The `tenant isolation` check in `verify` ([tools.md](tools.md)), which runs the store and session cases against the shipped settings.

## Not here yet

- Routes, cookies and request limits: P2-05.
- The sign-in screen, and the proof of passkeys in a real browser: P4-01.
- A list of signed-in devices with a way to end one: the settings screens, P11-01. The session id is there for it.
- An audit entry for each sign-in and sign-out: P2-04 builds the audit log, and P2-05 writes to it.
