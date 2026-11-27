# The API shell

Folders: `internal/api`, `internal/app`

The API is the one door to the data folder (decision [0004](decisions/0004-http-json-sse.md)). The web interface and the command line are both clients of it. This part is the shell every route sits in: it reads the session, finds the tenant, records the call, and gives the route's handler one tenant's store and nothing else.

- `internal/api` is the mechanism: what a route is, the handler that serves a list of routes, the session cookie, the shape of an error, server-sent events, the OpenAPI document, and the shell's own routes. It reads no setting.
- `internal/app` is the one list of every route Keystone serves, and the place the API's settings are read. The binary serves `app.Handler`, the `verify` walks read `app.Routes`, and the OpenAPI document is built from the same list.

## What happens to a request

Everything under `/api/` goes through `api.Handler`, in this order.

1. A request id is made and returned in the `X-Request-Id` header. The server chooses it. An id sent by the client is not used.
2. The body is limited to `server.max_body_bytes`.
3. A write that a page on another site set off is refused with 403. This is Go's `http.CrossOriginProtection`, which reads the headers a browser adds. A client that is not a browser sends none and is let through.
4. If the request is for a door route, that route is served. A door route is one a person needs before a session exists.
5. Everything else, a path that is no route included, goes through one `audit.Wrap` ([audit.md](audit.md)). The session cookie is read, `signin.Resolve` turns the token into the one tenant's store ([signin.md](signin.md)), and the call is written in that tenant's audit log before the route runs. With no session the answer is 401 and nothing runs.
6. The route's handler is called with an `api.Call`: the store, the session and the request id.
7. The store is closed, and one line goes to the server's own log.

Step 5 is the only place a request is tied to a tenant. A handler never sees the token, and it is given no install to open another store from. Nothing else in the request, such as a header, a query parameter or a body field that names a tenant, is read for that purpose.

A store is opened for each call and closed after it. Decision [0040](decisions/0040-one-wrap-and-a-short-list-of-doors.md) says why the routes are laid out this way.

## Adding a route

A part that brings routes exports a function that returns them, and `internal/app/app.go` adds them to the list in `Routes`:

    all = append(all, chat.Routes(...)...)

A route is a row of data:

    api.Route{
        Method:  "GET",
        Path:    api.Prefix + "chats/{id}",
        Summary: "One chat.",
        Reply:   Chat{},
        Handle: func(w http.ResponseWriter, r *http.Request, c api.Call) {
            chat, err := Load(r.Context(), c.Store, r.PathValue("id"))
            ...
            api.WriteJSON(w, http.StatusOK, chat)
        },
    }

- `Path` starts with `/api/v1/`. A part in braces is read with `r.PathValue`.
- `Handle` gets the `api.Call`. `c.Store` is the tenant's store. `c.RequestID` is also in the request's context, where `audit.RequestID(ctx)` finds it for the model calls and tool calls made for this call.
- `Body`, `Reply`, `Query` and `Events` describe the route in the OpenAPI document. `Body` and `Reply` are values of the Go types the route reads and writes.
- `api.ReadJSON(w, r, &body)` reads the body. When it returns false it has already answered.
- `api.Error(w, r, status, code, message)` answers with the error shape.

`api.Check` refuses a route outside the prefix, a method that is not one, the same route twice, a route with no summary, and a route whose handler does not match its side of the door. `api.Handler` runs it, so a bad list stops the server from starting.

After adding or changing a route, bring the committed document up to date:

    go run ./cmd/keystone openapi --out docs/api/openapi.json

Since P9-02 a route may take a larger body than `server.max_body_bytes`: its `MaxBodyBytes`, when it is larger, is the limit for that route alone, found by the shell's own mux before the body is read. The upload route uses it ([uploads.md](uploads.md), decision [0384](decisions/0384-a-route-may-take-a-larger-body.md)).

## Door routes

A door route is served before sign-in. It sets `Door: true` and has `Open` in place of `Handle`. It is given no store.

There are two, and both are for signing in. `tools/verify/routes.go` repeats the list. A route marked as a door that is not on that list fails the `tenant isolation` check, so a door cannot be added in one place only.

Since P10-04 there are four: the two share doors, `GET /api/v1/shared/{token}` and `GET /api/v1/shared/{token}/images/{image}`, show one chat through a share link to someone who has not signed in. Each finds its one tenant from the link's token alone, with `share.Find`, the way `signin.Resolve` finds a session's; reads no cookie and sets none; and writes each call that finds a link in that tenant's audit log under the route's pattern, never the token. See [share.md](share.md) and decision [0470](decisions/0470-a-share-link-is-a-door-that-finds-its-tenant-from-its-token.md).

## The admin door

Since P15-01 (provisional on the owner's answer A1), a route under `/api/v1/admin/` (`api.AdminPrefix`) is marked `Admin`, and `api.Check` refuses a route whose mark and path disagree, a door that is an admin route and a `StepUp` route that is not one. Between `audit.Wrap` and the routes, the shell refuses every request to a path under the prefix, a route or not, with 403 `not_admin` when its person does not carry the admin mark, read from `keystone.json` for that call, and with 401 `step_up` when their session has no fresh step-up, unless the route is marked `StepUp`. Such a call is recorded with the actor `admin` when the person carries the mark, and `person` when not. A handler of an admin route gets `Call.Admin` true. The routes and the rest are in [admin.md](admin.md); decision [0642](decisions/0642-the-admin-door-is-a-mark-on-a-route-checked-by-the-shell.md) says why.

`GET /api/v1/session` and the sign-in answer gain `admin` and `session.admin_until`.

## Errors

Every answer that is not a success has this body, `api.ErrorBody`:

    {"error": "sign in first", "code": "no_session", "request_id": "5f0c2a9d1b7e4c33"}

`error` is for a person. `code` is a short fixed name a client can test. The codes the shell uses:

| Code | Status | Meaning |
| --- | --- | --- |
| `no_session` | 401 | There is no session, or it has ended. |
| `refused` | 401 | Sign-in was refused. It never says why. |
| `wait` | 429 | Too many wrong passwords in a row. `Retry-After` says how long. |
| `cross_origin` | 403 | A page on another site tried a write. |
| `not_admin` | 403 | An admin route, and the person does not carry the admin mark (P15-01). |
| `step_up` | 401 | An admin route, and the session has no fresh step-up. The session itself is still good (P15-01). |
| `bad_request` | 400 | The body or a parameter is not what the route takes. |
| `too_large` | 413 | The body is over `server.max_body_bytes`. |
| `no_route` | 404 | There is no such route. Only a signed-in caller is told so. |
| `not_recorded` | 500 | The call could not be written in the audit log, so it was not run. |
| `failed` | 500 | The call failed. The reason is in the server's log under the request id. |

## The session cookie

Sign-in answers with the session token in a cookie named `keystone_session` and in no other place. The body of the answer describes the session and does not hold the token.

The cookie is `HttpOnly`, so the page's scripts cannot read it. It is `SameSite=Strict`, so the browser sends it only with requests from Keystone's own pages. Its path is `/api/`, so it is not sent with requests for the page's files. It ends when the session does. It is marked `Secure` when the request came over HTTPS or when `server.secure_cookie` is on. The setting ships off, because Keystone is reached over plain HTTP on the home network today.

The command-line client sends the same cookie. There is no second way to present a session, such as an `Authorization` header.

## Server-sent events

A route that streams calls `api.OpenStream`:

    s, err := api.OpenStream(w, r)
    if err != nil {
        return
    }
    defer s.Close()
    s.Send(api.Event{ID: "17", Name: "text", Data: chunk})

- An event is an id, a name and data. The data is written as JSON on one line.
- The id is what a client sends back in `Last-Event-ID` when it connects again after a dropped connection. `api.LastEventID(r)` reads it. Resuming from an id is the route's own work.
- While the stream is open, a comment line (`: beat`) is sent every `server.stream_heartbeat`, so nothing between Keystone and the browser closes a quiet connection. `Close` stops it, and the handler must call `Close` before it returns.
- `Send` may be called from more than one goroutine. A `Send` that fails means the client has gone. So does `r.Context().Done()`.
- A stream passes through the audit wrapper as it is written. The call's end is recorded when the handler returns, also when the client left first.
- A stream route lists its events in `Route.Events`, and they appear in the OpenAPI document under `x-keystone-events`.

`GET /api/v1/stream/test` is a stream that counts. It is there so a client's event reader, and its use of `Last-Event-ID`, can be built before any real stream exists.

## The OpenAPI document

`api.Document` builds the document from the list of routes, so it cannot describe a route the server does not serve. The shapes come from the Go types in each route's `Body`, `Reply` and `Events`, read through their `json` tags.

It is in three places, all from the same list:

- `docs/api/openapi.json`, committed. This is the contract the interface lane builds against. A test in `internal/app` fails when the file is not what the routes say.
- `GET /api/v1/openapi.json`, for a signed-in caller.
- `keystone openapi`, which prints it without a server.

## The shell's own routes

| Route | Session | What it does |
| --- | --- | --- |
| `GET /api/v1/signin/people` | door | The people who can sign in, for the sign-in screen. Empty when `signin.list_people` is off. |
| `POST /api/v1/session` | door | Sign in with a tenant id and a password. Sets the cookie. |
| `GET /api/v1/session` | needed | Who is signed in, and until when. |
| `DELETE /api/v1/session` | needed | Sign out. |
| `GET /api/v1/audit` | needed | The person's own audit log, a page at a time. |
| `GET /api/v1/openapi.json` | needed | The OpenAPI document. |
| `GET /api/v1/stream/test` | needed | A stream that counts. |

Sign-in is not inside `audit.Wrap`, because it has no session yet. It writes its own entry in the audit log of the tenant the sign-in was for: `ok`, or `refused` for a wrong password or a wait. A sign-in that cannot be recorded is undone and answered with 500. A sign-in for a tenant that does not exist has no log to go in. It is answered exactly as a wrong password is, and noted in the server's log.

How the sign-in screen asks who is signing in is an open question for the owner. Until it is answered, the sign-in route is told the tenant by its id, and the people route lists the people behind a setting. Both are provisional.

## The server's log

One line for each call, on standard error: the time, the request id, the method, the path, the status, how long it took and the tenant. It holds the path and never the query string, a header or a body. Each line goes through `audit.Scrub` first, which now also removes anything shaped like a session token.

A call with no session is in no tenant's audit log, because it has no tenant. This log is where it is recorded.

## Settings

All in `settings/core.json`, all for the whole install. `app.ReadConfig` reads them, with the sign-in settings.

| Key | Default | Meaning |
| --- | --- | --- |
| `server.secure_cookie` | false | Mark the session cookie for HTTPS only. |
| `server.max_body_bytes` | 1048576 | The most bytes read from one request's body. |
| `server.stream_heartbeat` | 15s | How often an open stream shows it is still there. |
| `server.page_limit` | 200 | The most rows a list returns in one call. |
| `signin.list_people` | true | Whether the door lists the people on the install. |

The server also reads `secrets.keeper` and passes it to `Install.NewInstallKeeper`, because the tenant package reads no setting ([secrets.md](secrets.md)).

**A route that serves a page or a file** sets its own headers over the server's. Since P9-03 the artifact sandbox route (`GET /artifacts/{artifact}/versions/{version}/sandbox`) answers with a policy of its own in place of `default-src 'self'`, `X-Frame-Options: SAMEORIGIN` in place of `DENY`, and `Cache-Control: private, no-cache` with an ETag in place of `no-store`, for that answer alone; the download route answers under `default-src 'none'; sandbox` as an attachment, as the upload content route does. See [artifacts.md](artifacts.md).

## Checked by

- `go test ./internal/api ./internal/app`. Every route but the door is refused with no session. A handler is given its session's tenant and no other, whatever else the request says. Sign-in sets a cookie scripts cannot read and puts the token in no body. Sign-ins are in the right tenant's log. A write from another site, a body over the limit and a body that is not the route's JSON are refused. A panic is answered and recorded. The log names each call and holds no token. A stream arrives as it is sent, resumes from `Last-Event-ID`, sends its heartbeat, and is recorded when the client leaves. The committed OpenAPI document matches the routes.
- The `tenant isolation` check walks every route: refused with no session, with a session re-addressed to another tenant and with a signed-out session, and written in the caller's audit log and not in the other tenant's ([tools.md](tools.md)).
- The `calls recorded` check: every route called with a session leaves an entry from before the call and an entry for its end.
- The `no keys in output` check: a stored secret and the session token are looked for in every route's answers, the server's log and the data folder.

## Not here yet

- Passkey routes. Passkeys are off until the sign-in screen proves them in a browser (P4-01).
- Routes that change a password, a setting or a secret, and a list of signed-in devices: the settings screens (P11-01).
- Install settings that an admin has changed. There is nowhere to store one yet, so the server runs with the registry's defaults.
- A person added while the server runs is not seen until it starts again. The server reads the tenant registry once.
- A limit on how often the door may be tried. A wrong password already makes that tenant wait ([signin.md](signin.md)), but each try still adds a row to that tenant's audit log.
- HTTPS. Phone access from outside the home (P10-05) decides how Keystone is reached, and then `server.secure_cookie` goes on.
