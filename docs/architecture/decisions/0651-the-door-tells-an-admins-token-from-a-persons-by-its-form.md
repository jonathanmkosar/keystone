# 0651. The door tells an admin's token from a person's by its form, before anything is read

November 27, 2026 · decided in P15-01 · changes decision [0642](0642-the-admin-door-is-a-mark-on-a-route-checked-by-the-shell.md)

## The question

With a separate admin account (decision 0650) there are two kinds of session. The spec asks that a person's token is refused on every admin route and an admin's on every person's route, told apart by the door before anything is read. Where does the door live, how does a browser signed in as both keep them apart, and where is each refusal recorded?

## The decision

- **Two forms.** A person's token is `kss_<tenant id>_<64 hex>`; an admin's is `ksa_` and 32 random bytes as hex, with no account in it (the admin database finds it by its hash).
- **Two cookies.** The person's stays `keystone_session`, path `/api/`. The admin's is `keystone_admin`, path `/api/v1/admin/`, so a browser sends it with admin routes alone. Both are `HttpOnly` and `SameSite=Strict`.
- **The pick, by form alone.** After the door routes (the person's sign-in, the share links, the admin's sign-in), the shell looks at the cookies' text and opens nothing: on a path under `/api/v1/admin/` a token of the admin form is taken from the admin cookie first; elsewhere from the person's cookie first, and from the admin cookie only when the person's is empty. A token of the admin form goes to the admin side; everything else to the person's side, as before.
- **The admin side** (`internal/api/admin.go`) opens the admin database, checks the session, writes the call in the admin audit log before it runs and when it ends, with the actor `admin:<account id>`, and then runs an admin route with `api.Call.Admin` (the admin database) and `Call.AdminSession`, and no tenant store; a person's route, or any path outside the prefix, is refused there, 403 `admin_account`.
- **The person's side** keeps `audit.Wrap` and `signin.Resolve`, and in front of the routes refuses every path under the prefix, 403 `not_admin`, recorded in the person's own log like any other call.
- **The route mark stays.** `api.Route.Admin` marks every route under `/api/v1/admin/` and no other, and `api.Check` holds the two together. An admin route may now be a door: the admin's sign-in and its passkey prompt. `StepUp` is gone.
- **The server's log** names the admin account on an admin call (`admin=<id>`) beside `tenant=-`.

## Why

- Telling the two apart by text means a person's token never opens the admin database and an admin's never opens a tenant: neither side reads the other's store even to refuse it.
- A path-scoped cookie lets the owner be signed in as himself and as the admin in one browser, each on its own routes, with no client code choosing which to send.
- Recording a person's refused admin call in their own log, and an admin's refused person's call in the admin log, keeps the fourth fixed rule: every call is recorded somewhere its maker can be found. The existing walks (`tenant isolation`, `calls recorded`, `no keys in output`, the isolation drill) needed no change: with a person's session every admin route answers 403 and is in that person's log.

## What it costs

- A person's token placed by hand in the admin cookie is no session there, and is refused 401 without being written anywhere, as a missing token is.
- An admin's token on a person's route is refused only after its session is checked, so an expired admin token there is a 401, not a 403.
