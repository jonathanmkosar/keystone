# P4-03. Streaming turns

Status: done
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

### Result

Done on November 25, 2026, in three commits on `chats/p4-03-chat-turn`: the contract, the build, and the write-up.

- Twelve routes under `/api/v1/chats`, in `internal/turn`, described in `docs/architecture/turns.md` and `docs/api/openapi.json`. No shape changed between the contract commit and the build.
- A turn and its events are rows in the tenant's database (`20261125T030000_turns.sql`). The runner works a chat's turns one at a time with a store of its own. Decisions 0140 to 0144.
- The proof passes in `go test ./internal/turn`, against a real HTTP server from `httptest`: a turn streams to a client and resumes after `Last-Event-ID`; stop ends the model call and keeps what streamed; a server stopped with one turn running and one waiting is started again, and the waiting turn finishes.
- `verify` is 15 of 15. `calls recorded` now sends a message as the test tenant and finds the model call in the audit log, before and after, under the request id of the call that sent it.

Left for the items that own them, and listed in the doc: tools (P7-01), the context builder (P5-01), routing and the spend cap (P3-08), the real adapters in the server's set (P3-04 to P3-06), uploads (P9-02). Until P7-01 a tool call is answered with an error. The title call was tested with a stand-in provider and has not been run against a real model.

