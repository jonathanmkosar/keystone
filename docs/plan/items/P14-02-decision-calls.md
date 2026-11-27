# P14-02. Decision calls and the decision adapter

Status: provisional (models), on the live check: Ollama 0.35.1 or later with the decision model pulled on the owner's machine. Everything else in the proof passes on recorded exchanges and the fake provider (journal models 010).
Lane: models
Depends on: P3-02, P3-03, P3-08
Owner gate: Ollama 0.35.1 or later on the owner's local machine, with the decision model pulled (live check only).
Built without the gate: All code, proven on recorded exchanges and the fake provider.
Scope: The capability `decision`, the seam's `Decide` call and its types, the `systemone` adapter, decision answers in the fake provider, and the audit entries and usage records of a decision call. The kind `decision` in `routing.Plan`, so a sensitive decision call is routed by the existing guard.
Proof: A recorded decision call returns each option's probability. A request over the size or question limit is refused before it is sent. A decision call with sensitive state never reaches a hosted entry. Every decision call has its audit entries and usage record.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Decision calls on the seam", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `models/p14-02-decision-calls`. Seam changes stay in new files so P14-01 (effort, `seam.Request`) merges cleanly.

1. **Registry.** `models.CapDecision` (`decision`) and the adapter `systemone`. An entry with `decision` lists no other capability; a `systemone` entry must have `decision`.
2. **Seam** (`internal/seam/decide.go`): `DecisionRequest` (model, state, questions), `DecisionQuestion` (id, instructions, type `choice` or `yes_no`, options as id and description), `DecisionResult` (one answer per question: the choice and a probability for every option, and usage), the `Decider` interface, and their checks.
3. **The `systemone` adapter** (`internal/adapters/systemone`): `POST {endpoint}/v1/systemone` in Ollama's wire shape (questions keyed by id, `criteria`, `noul` for yes or no). Refuses before sending a request over `systemone.max_request_bytes` or with more than `systemone.max_questions` questions; one attempt within `systemone.timeout`. Its `Stream` refuses: the entry takes decision calls only. In `adapters.Real`.
4. **The fake**: a script whose `when` has `"decision": true` answers decision calls, with one step holding a `decision` result or an `error`. `adapters.OpenDecider` and `adapters.Decides`.
5. **Routing**: the kind `decision`; a decision model takes decision calls and nothing else, as an embedding model does. A chat turn never lists one as a candidate.
6. **Recording** (`internal/decision`): `Choose` asks `routing.Plan` for the decision model, so a sensitive call never reaches a hosted one; `Run` writes the started and end audit entries and a usage record of the kind `decision` for every call, refused ones included, and refuses a sensitive call to a model that may not read it a second time. A local model costs nothing.
7. **Proof**: recorded exchanges in `fixtures/systemone/`, fake decision scripts, unit tests, and a decision case in the `sensitive content` check.

Settings: `systemone.timeout` (5s), `systemone.max_request_bytes` (65536), `systemone.max_questions` (64), all install. The router's own settings (`router.*`) are P14-03's.
