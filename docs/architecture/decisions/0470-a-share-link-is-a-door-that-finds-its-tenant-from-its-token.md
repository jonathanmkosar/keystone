# 0470. A share link is a door that finds its tenant from its token

November 26, 2026 · decided in P10-04

## The question

A share link must open for someone who has not signed in, so its route cannot sit inside the audit wrapper that resolves every other route's tenant from the session (decision 0040). Decision 0040 foresaw this: sharing "is served by a door of its own, which will need a line on the list". How does such a door find the one tenant, and only that tenant, without a session and without a table that holds rows of every tenant (decision 0003)? And how is its token kept out of the logs, when it travels in an address?

## The decision

- **Two door routes**, `GET /api/v1/shared/{token}` and `GET /api/v1/shared/{token}/images/{image}`, listed in `tools/verify/routes.go`. They are given no store by the API shell, read no cookie and set none.
- **The token names its tenant**, as a session token does (decision 0007): `ksh_`, the tenant's id, `_`, and 32 random bytes as hex. `share.Find` reads the id, checks it is a tenant on the install, opens that one store, and looks there for the SHA-256 of the whole token. No other store is opened and nothing else in the request is read. The database keeps the hash only; the token is in the answer that makes the link, once.
- **A package of its own enforces it.** `internal/share` holds the token, `Find`, the refusals and what a link shows, and is listed as a guard of fixed rules 1 and 3, so it may not reach the settings registry. `internal/shareapi` holds the routes and reads the share settings for the tenant a call is for.
- **Every call that finds a link is recorded** in that tenant's audit log, as sign-in records itself: the route's pattern as the action (never the path, which holds the token), `share:<link id>` as the actor, the status. A view that cannot be recorded is not served. A token that finds nothing has no tenant to be recorded under, and goes in the server's log only, as a sign-in for an unknown tenant does.
- **The token is kept out of the logs** by its shape: `audit.Scrub` removes anything shaped like a share token (one line beside the session token's), so the server's log line, which holds the path, never holds it, and the door names it as a secret to `audit.Append` too. `Referrer-Policy: no-referrer`, already on every page, keeps it from leaving with a link the shared chat holds.
- **The page reads it without credentials.** The shared page fetches the view with `credentials: "omit"`, so the browser does not even send a session cookie with it; the server would not read one if it did.

## Why

- The same shape as the session token means the same proof and the same probes: re-addressed, forged and copied-row tokens find nothing, for the same reasons, and the tenant isolation walk has one more probe rather than a new idea.
- Looking in every tenant's database for a hash would touch every tenant's data on every unauthenticated request; a shared index would be a table across tenants. Both were ruled out for sessions, for the same reasons.
- A token in the path, rather than in a header or the fragment, is what lets an image be an `<img src>` under the main page's policy, which allows no `blob:` and no `data:`. The scrub keeps it out of Keystone's logs either way.
- Splitting the rules from the routes keeps the guard free of the settings registry, which `internal/chat`, `internal/api` and `internal/uploads` all reach; the guard reads the chat record with its own two queries.

## What it costs

- The tenant's id is readable in a link. It opens nothing, as decision 0007 says of the session token.
- The token is in the address bar and the browser's history of whoever opens the link, and in any log in front of Keystone (the owner's Cloudflare logs paths). Turning a link off is the remedy for a link that went further than meant.
- A link is shown once. A person who loses it makes another; only its hash could be shown again, which is no use.
- The doors can be tried by anyone who can reach the address, and each try that finds a link adds a row to that tenant's append-only log. A forged token adds none. A limit per address belongs with phone access from outside (P10-05).
