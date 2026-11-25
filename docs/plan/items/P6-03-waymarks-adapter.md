# P6-03. Waymarks adapter

Status: doing (knowledge)
Lane: knowledge
Depends on: P6-01
Owner gate: none
Scope: API client for one Waymarks instance per tenant; one token per role (reader for chat and sub-agents, intake for the memory pass); outage handling that never breaks a chat.
Proof: Passes the contract suite against a Waymarks test tenant; a chat finishes with Waymarks down.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan (knowledge lane, November 24, 2026)

What will change:

- A new package, `internal/knowledge/waymarks`, registered as `waymarks`. It is an HTTP client that implements `knowledge.Store` for one Waymarks instance, written against Waymarks' own API as its source describes it (`/v1`, bearer tokens, `{"data": ...}` answers with a `sensitive` mark).
- The instance's address and the limits are settings in `settings/knowledge.json`. `waymarks` joins the options of `knowledge.adapter`.
- The tokens are read by name from the tenant's vault through `Store.Secrets()`: a reader, an intake token and an embedder. Capabilities are set from which of them are there. Reads go out under the reader, `AddSource` under the intake token, and the two vector calls under the embedder.
- The label of what a call returns comes from the same answer where Waymarks gives it, and from the list of spaces read after the content where it does not, so a space made sensitive is never read as normal.
- An instance that cannot be reached, is slow, refuses the token or answers with something that is not Waymarks returns `knowledge.ErrUnavailable` inside the timeout setting. The client follows no redirect and no error carries a token.
- `internal/knowledge/waymarks` joins `networkAllowed` in `tools/verify/offline.go` and `componentDocs` in `tools/verify/main.go`.

How it will be proven:

- The contract suite passes against a fake Waymarks served by `httptest` inside the package's tests, with all three tokens, with the reader and intake tokens, and with the reader alone.
- A server that is down, one that never answers and one that answers with rubbish each yield `ErrUnavailable`, quickly.
- Each call goes out under the token of its role, and no token appears in an error.
- `go run ./tools/verify` is green.

What cannot be proven today, so the item ends provisional:

- "A chat finishes with Waymarks down" needs the chat turn (P4-03).
- The contract suite against a real Waymarks test tenant needs an instance and its tokens, which only the owner can give.

