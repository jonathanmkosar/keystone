# P4-03. Streaming turns

Status: todo
Lane: chats
Depends on: P2-05, P3-03, P3-09, P4-02
Owner gate: none
Scope: Turn API from message to model seam to stream; stop; durable queued turns that survive a restart; automatic titles on a cheap model.
Proof: A turn streams to the client; stop ends the model call; a queued turn finishes after a restart.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

