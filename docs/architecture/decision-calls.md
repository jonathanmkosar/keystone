# Decision calls

A decision call (P14-02) sends a state and typed questions to a decision model, which answers each question with a probability for every option. The router's verdict (P14-03) is one: which step of the ladder, and which effort. This page is the whole of it; the seam's types are in [seam.md](seam.md), the adapter in [adapter-systemone.md](adapter-systemone.md), and the design in `docs/plan/SPEC-router-and-admin.md`, Part 1, "Decision calls on the seam".

## The parts

| Part | Where | What |
| --- | --- | --- |
| The capability `decision` | `internal/models` | An entry with it is a decision model. It lists `decision` alone, and takes decision calls and nothing else. |
| The seam's call | `internal/seam/decide.go` | `DecisionRequest`, `DecisionResult`, the `Decider` interface and their checks. |
| The kind `decision` | `internal/routing` | `Plan` routes a decision call to decision models only, and a sensitive one only to those that may read sensitive content. |
| The adapters | `internal/adapters/systemone`, `internal/adapters/fake/decide.go` | Ollama's decision API, and the fake's recorded decisions. `adapters.OpenDecider` opens either; `adapters.Decides` says which adapters decide. |
| Choosing and recording | `internal/decision` | `Choose` and `Run`. A guard of fixed rule 3. |

A chat never offers a decision model: the turn's candidates leave out entries with `decision`, as they leave out embedding models, and `routing.Plan` withholds one from every other kind of call as `capability`.

## Making a call

    usable, _ := models.Usable(ctx, store)
    can := func(m models.Model) bool { return set[m.Adapter] != nil && adapters.Decides(m.Adapter) }
    m, err := decision.Choose(name, sensitive, usable, can)   // ErrNoModel: the rules alone decide
    m, d, err := adapters.OpenDecider(ctx, store, set, m.Name)
    res, err := decision.Run(ctx, store, decision.Opened{Model: m, Decider: d, Secrets: keys},
        decision.Call{RequestID: id, Actor: actor, Sensitive: sensitive, Request: req},
        decision.Options{Gate: gate, Estimate: estimate})

`internal/decision` imports neither `internal/adapters` nor any setting, so the opening is the caller's: the router reads `router.decision_model`, the cap and the budget, and passes them in.

### Choose

`Choose(name, sensitive, usable, can)` asks `routing.Plan` with the kind `decision`, the name as the rule's model and the ask's, and the sensitive mark. It returns the one model, or an error wrapping `decision.ErrNoModel`:

- no name;
- the name is not a usable decision model that `can` says this build can open;
- the call is sensitive and the model may not read sensitive content (with `routing.ErrSensitive`).

There is no fallback to another decision model. The spec says a verdict that cannot be had is left to the rules, and a second decision model would only add a wait (decision [0633](decisions/0633-decision-calls-are-recorded-by-their-own-guard-package-with-no-fallback.md)).

### Run

`Run(ctx, store, opened, call, options)` makes the call and records it as fixed rule 4 asks:

1. A request that is not valid, or names a model other than the one opened, is refused as `invalid`. It is not a call: nothing is sent or recorded.
2. A started entry in the audit log, `kind` model, the action the model's registry name, `sent` the request's JSON. A call that cannot be recorded is not made.
3. **Fixed rule 3, a second time.** A sensitive call to a model that may not read sensitive content is refused, whatever chose it: recorded as refused and never sent.
4. `Options.Gate`, when given, is asked: the monthly cap, for the router. A refusal is recorded as refused and returned.
5. The call. An error is recorded with its kind: `too_long` (the adapter's limits) and `spend_cap` as refused, anything else as an error.
6. An answer that does not answer the request (`seam.ValidateDecisionResult`) is recorded as an error and refused as `other`.
7. A count of zero is estimated with `Options.Estimate`, when given, and marked estimated. `decision.Text` and `decision.AsRequest` give the request as text and as a `seam.Request`, for the budget's estimate and the cap's most cost.
8. An end entry with the result's JSON, the usage, the cost and the time, and one usage record of the kind `decision` with its outcome. If the end of an answered call cannot be written, the caller gets the error and not the answer.

Every call that got past step 1 has a started entry, an end entry and a usage record, however it ended. That is the proof's fourth line.

### What it costs

A local decision model costs nothing, whatever its entry's price says: its cost is written as 0. A trusted or hosted one costs its input and output tokens at its price, is counted under the monthly cap, and is gated by it when the caller passes the cap as the gate. A hosted decision model is out of the spec's scope, and a sensitive call never reaches one.

## The fake

A script whose `when` has `"decision": true` answers decision calls, and only those; a script without it answers only turns. Its one step is a `decision` (a `seam.DecisionResult`) or an `error`. For a decision call the state is matched as the user text, and the questions' instructions, one to a line, as the system text. The answer is checked against the request, as a real endpoint's is. `fixtures/fake-provider/decisions.json` holds a sure and an unsure verdict, a yes or no, and a decision model that is down ([fake-provider.md](fake-provider.md)).

## Settings

The adapter's own: `systemone.timeout` (5s), `systemone.max_request_bytes` (65536) and `systemone.max_questions` (64), all install ([adapter-systemone.md](adapter-systemone.md)). The router's (`router.decision_model`, `router.verdict_timeout` and the rest) come with P14-03.

## Checked by

- `go test ./internal/decision`: `Choose` keeps a sensitive call off the hosted decision model and refuses a name that is not a usable decision model; every call (answered on a local and on a trusted model, sensitive to a hosted one, stopped by the gate, over the adapter's limit, the endpoint down, an answer that does not answer) has a started entry, an end entry and one usage record of the kind `decision` with the right outcome and cost; an estimate is marked; a request that is no call is not recorded.
- `go test ./internal/routing`: the kind `decision` routes to decision models only, a sensitive one never to a hosted model even when a rule names it, and a decision model is on no other kind's route.
- `go test ./internal/adapters/...`: the systemone exchanges and limits, the fake's decision scripts, `OpenDecider` and `Decides`, the adapter in the real set with the spec's settings.
- The `sensitive content` check in `verify` (`tools/verify/decisioncalls.go`): on an invented person with a local, a hosted and a trusted decision model on the systemone adapter's name, answered by the fake's recorded decisions, a sensitive verdict named for the hosted model is refused by `Choose` and by `Run`, and the hosted model never hears it; the local and the trusted model answer it with each option's probability; a plain verdict reaches the hosted model; each call has its two audit entries and its usage record.
- `fixed rules` in `verify`: `internal/decision` is a guard of the sensitive content rule and reads no setting.

## Not here yet

- **The caller.** The router (P14-03) chooses, opens and runs the verdict, with its own timeout, gate and estimate. Nothing calls `Run` in the server yet.
- **The live check**, on the owner's Ollama with the decision model pulled.
- The browser's model picker lists a registry entry with `decision` for a new chat (it filters embedding models only, in `web/app/src/ModelPicker.tsx`); the server refuses to route a turn to it. The interface lane adds the filter.
