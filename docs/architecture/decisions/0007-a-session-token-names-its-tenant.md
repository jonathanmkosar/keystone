# 0007. A session token names its tenant

November 24, 2026 · decided in P2-02

## The question

Every route gets a session token and must turn it into one tenant's store. Sessions live in each tenant's own database, because decision 0003 allows no database that holds rows from more than one tenant. So which database does the server look in?

Three ways were weighed.

1. A shared list of sessions beside `keystone.json`. It would be a store with rows from every tenant, which 0003 rules out. It would also be one more file to back up, restore and keep in step with each tenant's folder.
2. Look in every tenant's database until the token is found. Every request would touch every tenant's data, and get slower with each tenant. A mistake in that loop is a mistake across tenants.
3. The token says which tenant it belongs to.

## The decision

The token names its tenant. It is `kss_`, the tenant's id, an underscore, and 32 random bytes as 64 hex characters.

- The server reads the id from the token, opens that one tenant's store, and looks there for the SHA-256 of the whole token. No other database is opened.
- The id in the token is a claim, not proof. The proof is that the token's hash is in that tenant's database. Writing another tenant's id into a token changes its hash and points it at a database that never held it.
- `Check` refuses a token that names any tenant but the store's, before it reads anything. So a session row that ends up in the wrong database, by a mistaken restore or a bug, opens nothing there.
- The database keeps the hash and never the token. The token is 256 random bits, so a plain SHA-256 is enough: there is nothing to guess.

Two smaller choices made with it:

- **No table of users.** The plan says a tenant and a user are the same. A tenant has one password at most and any number of passkeys. There is no user name to store, and nothing to look up across tenants.
- **Passwords are hashed with PBKDF2-SHA-256**, 600,000 rounds as shipped, a setting. It is in Go's standard library. Argon2id, scrypt and bcrypt are in `golang.org/x/crypto`, which would be a second outside module. The stored row names its algorithm and its rounds, so a later item can move to another without anyone setting a new password.

## What it costs

- The tenant's id can be read from the token. The id is not a secret: it is a folder name, and knowing it opens nothing.
- The token is a bearer token. Whoever holds it is signed in until it ends or is signed out. Keeping it in a cookie the page's scripts cannot read, and out of every log, is the API's job (P2-05).
- A session ends at a fixed time after sign-in, `signin.session_lifetime`. Using it does not make it last longer, so a person signs in again every thirty days as shipped. Making it slide would cost a write on every request.
- PBKDF2 is easier to attack with graphics cards than Argon2id is. The round count and the twelve-character minimum carry the weight meanwhile.
- A sign-in route has to know which tenant is signing in before it can check a password, since there is no shared list of names. How the sign-in screen asks is not decided here. It is an open question for the owner, recorded in the journal entry for P2-02, with a recommended answer: the screen lists the people on the install and the person picks their own name.
