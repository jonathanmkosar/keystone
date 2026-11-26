# 0352. The Google sign-in uses PKCE and returns to a page; the refresh token is in the vault and access tokens in memory

November 25, 2026 · decided in P12-02

## The question

Gmail and Calendar need a Google sign-in for each person: an OAuth 2.0 flow that ends with a refresh token Keystone keeps. Keystone runs on the owner's machine, reached on `127.0.0.1` and through his own domain, from a desktop or a phone. Where does Google send the browser back to, how is the flow tied to the person who started it, which scopes are asked for, and where do the client and the tokens live?

## The decision

**The authorization code flow with PKCE (S256)**, `access_type=offline`, `prompt=consent` so a refresh token comes back each time, and `include_granted_scopes=true`.

**Google sends the browser back to a page, not an API route**: the page's own origin with `/settings/connectors/google`, taken from the Origin of the same-origin POST that started the sign-in, or `agents.google_redirect_url` when the admin set one. The session cookie is SameSite=Strict, so a browser arriving from Google does not send it; the page, once loaded, posts the code and the state with the session it has, and takes them out of the address bar. No door route is added, so the tenant isolation check's short list of routes served without a session is unchanged.

**The state and the PKCE verifier are kept in the person's own database** (`google_pending`), for `agents.google_sign_in_window` (10 minutes), and used once. The finish looks the state up in the signed-in person's tables, so a code started for one person cannot be finished by another.

**The narrowest scopes**: `gmail.readonly` and `gmail.compose` (read; draft and send, which cannot read), `calendar.events.owned` (events on calendars the person owns), and `openid email` to name the account. They are constants beside the code that uses them, not settings: a narrower one breaks a tool, and a broader one is a code change with a reason.

**The OAuth client is the owner's**: its id in the tenant-admin setting `agents.google_client_id`, its secret in the person's vault as `google.client_secret`, both written by the admin from the command line. Until both exist the screen says Google is not set up and names what is missing.

**The refresh token is in the person's vault** as `connector.google`. `reachFor` reads it with the client for each call and gives the call an access token from an in-memory cache shared by the server's pool and routes, refreshed a minute before it would stop working. The refresh token, the client's secret and every access token handed out are taken out of everything Google sends back. Every Google address is a setting dialled as web_fetch dials: public addresses only, no proxy, no redirect.

## Why

- A same-origin page works from wherever the person reached Keystone, the domain or `localhost`, with one Web application client that has both addresses registered; a loopback-only redirect would not work from a phone.
- Making the callback a page keeps the session cookie Strict, which guards every other route against requests set off by other sites, and keeps the code out of the API's audit path (the audit log records methods and paths, never bodies).
- A state in the person's own database is tenant-scoped by construction, survives a restart within its window, and needs no shared memory.
- Install-scope settings have no override store yet (the server reads the shipped defaults), so the client id is tenant-admin: the admin sets it for each person with `tools/tenant setting set`. One client serves the whole household. The secret goes in each person's vault because no install-level vault exists, and reading the admin's vault for another person's turn would cross tenants (fixed rule 1).
- Access tokens last an hour; keeping them in memory only means a stolen data folder holds none, and a restart costs one refresh.

## What it costs

- The owner stores the client secret once per person who connects.
- `prompt=consent` shows Google's consent screen on every connect, the price of always getting a refresh token back.
- A browser that sends no Origin on a POST cannot start a sign-in unless the admin sets `agents.google_redirect_url`.
