# P15-05. The admin isolation drill

Status: todo
Lane: core
Depends on: P15-02, P15-03, P15-04, P15-06
Owner gate: None.
Built without the gate: Everything.
Scope: The second part of the `admin isolation` check: marker words in a second person's chats, titles, files, memory, traces and audit log, then every admin route called, and the server's log read. A standing check in `verify`.
Proof: No marker word appears in any admin route's reply or in the server's log. The check fails if one does.

See `docs/plan/SPEC-router-and-admin.md`, Part 2, "Reading another person", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
