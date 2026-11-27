# P15-01. The admin mark and step-up

Status: doing (core)
Lane: core
Depends on: P2-02, P2-04, P2-05
Owner gate: The owner's answers A1 and A2.
Built without the gate: Everything, on the assumed answers, provisional until they are confirmed.
Scope: The `admin` mark in `keystone.json` and the `tools/tenant admin` command, step-up and `admin_until`, the admin door, audit entries in both logs, `internal/adminview`, and the first part of the `admin isolation` check (the imports).
Proof: A person without the mark, and an admin without a fresh step-up, are refused on every admin route. Every admin call is in both audit logs.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "The admin mark" and "Reading another person", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
