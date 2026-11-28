# P14-04. Auto in the turn

Status: todo
Lane: chats + interface
Depends on: P14-03, P4-03
Owner gate: Approval of the chat mockups (P1-04), for the screen only.
Built without the gate: The server side in full; the picker, footnote and menu from the draft mockups, provisional.
Scope: The chat's `auto`, the router before every message, the choice held for the turn's tool steps, `router_verdicts`, the `stronger` route, the new fields of `ModelChoice`, and the `route` stream event. Contract first. Screen half: Auto in the picker, the footnote, the verdict in the inspector, and Try a stronger model.
Proof: Recorded turns of the test tenant reach every step. A held chat on Auto reaches no hosted model (added to the `sensitive content` check). A hard message followed by an easy one goes back down, and a turn's tool steps stay on one model. With the local machine not answering, the reply carries the notice, and a held chat fails with `local_unavailable`.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Auto in the turn", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
