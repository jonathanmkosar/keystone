# 0642. The admin door is a mark on a route, checked by the shell, and every refusal is recorded

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A1

## The question

Admin routes must refuse a person without the mark and an admin without a fresh step-up, every one of them, including routes later items add in a hurry. Where does that check live, and how is a refusal recorded?

## The decision

- **A route under `/api/v1/admin/` is marked `Admin`.** `api.Check` refuses a route under that prefix that is not marked, and a route outside it that is, so the mark and the path cannot disagree. A door route cannot be an admin route.
- **The shell checks it, in one place, for every path under the prefix.** Between `audit.Wrap` and the routes, a request to an admin path is refused with **403 `not_admin`** when its person has no mark, and with **401 `step_up`** when their session has no fresh step-up (decision 0643). A path under the prefix that is no route is refused the same way, so a person without the mark cannot learn which admin routes exist.
- **The step-up's own routes are the exception, and a short list.** A route marked `StepUp` needs the mark and not a fresh step-up: `POST /api/v1/admin/step-up` and `POST /api/v1/admin/step-up/passkey`. `tools/verify/adminisolation.go` lists them a second time, as `routes.go` lists the doors, and the `admin isolation` check fails when the two lists differ.
- **The actor.** A call to an admin path by a person with the mark is written in their audit log with the actor `admin`; any other call, a refused one by a person without the mark included, with the actor `person`. The refusal runs inside `audit.Wrap`, so it is recorded before and after like any call.
- A handler of an admin route is given `api.Call` with `Admin` true, and the same one store as any route: the admin's own.

## Why

- The same reasons as decision 0040: a rule that holds because of where a route sits holds for every route, and a list kept twice takes two edits to widen.
- 401 for an ended step-up keeps the spec's code; a client tells it from an ended session by its code, `step_up` against `no_session`.
- Recording the refusals puts a person's attempts on the admin area in their own log, where they can see them.

## What it costs

- The mark is read from `keystone.json` on each admin call (decision 0641), a small file read.
- A client must not treat every 401 as "sign in again": `step_up` asks for the password once more and keeps the session.
