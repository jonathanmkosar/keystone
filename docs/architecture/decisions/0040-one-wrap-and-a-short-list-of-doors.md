# 0040. Every route sits inside one audit wrapper, and the doors are a short list

November 24, 2026 · decided in P2-05

## The question

Two of the four fixed rules meet at the API. Every route must resolve its tenant from the session and from nothing else, and every call must be recorded. How is that made true for routes that five lanes will add over many months, without trusting each one to remember?

And sign-in cannot need a session. Where does it go?

## The decision

A route is a row of data, and one function, `api.Handler`, serves the whole list. A route does not register itself and cannot choose what wraps it.

- Everything under `/api/` that is not a door route goes through a single `audit.Wrap`. Its `who` function is the one place a session cookie is read and `signin.Resolve` is called. A path that is no route goes through it too, so a caller with no session cannot tell which paths exist.
- A route's handler is called with an `api.Call`: the store that session resolved to, the session, and the request id. It is not given the token or the install, so it has nothing to open another tenant's store with.
- A route served before sign-in is a door route. It is marked in the code, it is given no store, and it is outside the wrapper. The doors are listed a second time in `tools/verify/routes.go`. A door in the code that is not on that list fails the `tenant isolation` check.
- The list of every route is `app.Routes`, in a package of its own that the binary and `verify` both import. The walks call each route in it.
- Sign-in, being a door, writes its own entry in the audit log of the tenant it was for. A sign-in that cannot be recorded is undone.
- A tenant's store is opened for each call and closed after it.

## Why

- A rule that holds because of where a route sits holds for a route written in a hurry. A rule that holds because each handler calls two functions first holds until one forgets.
- Passing the store in, and nothing to get another one with, means a handler that wanted to reach another tenant would have to import the tenant registry itself. That shows in a review and in the imports.
- The list of doors is the list of ways in without a session. Keeping it in two places means widening it takes two edits, one of them in the checks, where it is read by whoever merges.
- `verify` cannot import the binary's `main` package. A package that only holds the list gives both the same routes, so a route cannot be served and not walked.
- A store for each call is the simplest thing that is right: `signin.Resolve` hands one back, the caller closes it, and no cache has to learn when a tenant is added or a session ends.

## What it costs

- Opening a store runs the migration check and opens the database file, a few milliseconds each call. A turn makes few calls and one long stream, so this is small today. If it shows in the load test (P13-03), stores can be kept open behind `signin.TenantOf` and `signin.Check`, which `signin.md` already allows, without changing a route.
- A handler cannot do work for two tenants in one call. Nothing in the plan wants that. Sharing a chat (P10-04) copies a snapshot out of one tenant and is served by a door of its own, which will need a line on the list.
- A sign-in for a tenant that does not exist is in no audit log, only in the server's log. There is no tenant to hold it.
- Each failed sign-in adds a row to that tenant's append-only log, and the door can be tried by anyone who can reach the address. On the home network that is the household. Reached from outside, the door needs a limit of its own, which belongs with the phone access decision (P10-05).
