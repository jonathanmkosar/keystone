# P4-03. Streaming turns

Status: doing (chats)
Lane: chats
Depends on: P2-05, P3-03, P3-09, P4-02
Owner gate: none
Scope: Turn API from message to model seam to stream; stop; durable queued turns that survive a restart; automatic titles on a cheap model.
Proof: A turn streams to the client; stop ends the model call; a queued turn finishes after a restart.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

1. The contract first, because the chat screen (P4-04) is built against it at the same time: the routes as `api.Route` values in a new package, `internal/turn`, with their types, registered in `internal/app`, answering 501. `docs/architecture/turns.md` has the table of routes, shapes and stream events and one worked example. `docs/api/openapi.json` is regenerated.
2. Then the build. A turn is a row in the tenant's database and so is each event it streams (one migration). A runner works each chat's turns one at a time in its own goroutine with its own store: it appends the person's message, builds a `seam.Request` from the current branch, opens the model through `adapters.Open` behind `budget.Guard` (the test tenant gets the fake provider and nothing else), writes events, the reply and the audit entries, and makes the title after the first reply. A client reads events from the table, so a dropped connection resumes with `Last-Event-ID`. Stop cancels the call and keeps what streamed. When the server starts, a turn that was running is closed as failed and a queued one runs.
3. Proof, in `go test ./internal/turn`: a turn streams to an `httptest` client with the fake provider; stop ends the model call; a queued turn finishes after a restart. The route walks in `verify` cover the new routes, and `calls recorded` gains a model call.

