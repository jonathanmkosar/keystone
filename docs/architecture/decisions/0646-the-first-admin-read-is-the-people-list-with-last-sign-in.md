# 0646. The first admin read is the people list, and a person's last sign-in is kept in their tenant_meta

November 27, 2026 · decided in P15-01 · provisional on the owner's answer A2

## The question

The item's proof is that every admin call is in both audit logs, which needs one admin route that reads another person. P15-02 owns the people and install routes. What does this item serve, and where does "last sign-in" come from?

## The decision

- **`GET /api/v1/admin/people`, its first part.** Each person's id, name, mark, when they were added and their last sign-in. P15-02 adds the numbers (spend, sizes, jobs, schedules, model health, watch levels) as more fields of the same `adminview.Person`, so the reply only grows.
- **Last sign-in is a row in the person's `tenant_meta`, `last_sign_in`,** written by `signin` each time a session is opened, by a password or a passkey. Before this item it was not kept; for a person who has not signed in since, `signin.LastSignIn` falls back to the newest session still open, and then to nothing.

## Why

- Sessions are deleted at sign-out and when they run out, so the newest session forgets a sign-in followed by a sign-out. The audit log holds every sign-in, but finding the newest in a log of hundreds of thousands of entries (P13-03 measured 438,169 for one person) would read most of it. One row written at sign-in is exact and costs one statement.
- Serving the route now, small, gives the door and `adminview` a real caller to be proven on.

## What it costs

- A person who signed in before this binary and has signed out since shows no last sign-in until they sign in again.
- P15-02 changes an existing route rather than adding it; the contract only grows.
