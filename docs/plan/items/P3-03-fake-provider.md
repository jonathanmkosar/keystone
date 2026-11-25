# P3-03. Fake provider

Status: done
Lane: models
Depends on: P3-01
Owner gate: none
Scope: A scripted model that streams, calls tools and thinks, driven by fixtures. Used by verify and the test tenant.
Proof: `verify` passes with no network and no keys; the offline check is added.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- `internal/seam` gets the one interface every provider implements: `Provider`, with one method, `Stream`, which takes a context, a `Request` and a function that receives each `Event`. `Call` runs a provider and folds the stream into a `Response`. Nothing that exists in the package changes.
- A new package, `internal/adapters/fake`: a `Provider` that plays scripts. A script has a name, a `when` that says which requests it answers, and steps. A step is a whole reply in the seam's own JSON, an error, or a reply that is cut part of the way through. Standard library only.
- Matching keeps no memory of earlier calls. The script is chosen by the last message the person wrote, with the system text and the model as optional conditions. The step is the number of assistant messages since that message, so the step after a tool call is reached when the tool result comes back. The same request always gets the same stream.
- A script is refused with a reason when its reply or its error is not valid in the seam's terms, when a step can never be reached, or when a file is not marked as test data.
- Example scripts in `fixtures/fake-provider/`, all invented: plain text, thinking, a tool call with its continuation, and failures.
- Two settings in a new `settings/models.json`: how big each streamed piece is, and how long the fake waits before each.
- The `offline` check in `verify`: only the folders on a short list import a network package, and the tests pass again with every key taken out of the environment and no way out to the network.
- Proof: tests for text in several pieces, thinking, a tool call and its continuation, each failure, each kind of refused script, and every example script folding back to its reply through the seam's collector. `verify` is green with the new check.
- Docs: `docs/architecture/fake-provider.md`, additions to `seam.md` and `tools.md`, a row in the overview, and decisions 0011 (matching) and 0015 (the shape of the interface).


### Result

Done on November 24, 2026.

- `internal/seam/provider.go` adds the `Provider` interface, which streams through a callback (decision 0015).
- `internal/adapters/fake` plays scripts from `fixtures/fake-provider/`: a plain answer, thinking, a tool call, and failures. A script is found from the request alone (decision 0011). It imports no network package and reads no key.
- `verify` has the `offline` check: only listed folders may import a network package, and the tests are run again with every key taken out of the environment and no way out to the network.
- The proof holds: `verify` passes with no network and no keys.
- Not proven yet, and said so in `tools.md`: a whole turn running offline through the fake provider. There is no turn until P4-03.
- The lane's session stopped after its code and docs were committed. The journal entry was written by the integrator.

Journal: `docs/journal/2026-11-24-models-002.md`.
