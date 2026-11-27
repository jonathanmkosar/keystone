# P14-08. The router's live check

Status: todo
Lane: models
Depends on: P14-04, P14-06
Owner gate: Ollama 0.35.1 or later with the decision model pulled; the largest hosted model registered; the owner's ladder set.
Built without the gate: Nothing: this item is the live check.
Scope: The owner's decision model and chat model on his local machine, and his hosted models, through Auto, with the local machine on and then off. The results go in the journal.
Proof: Each step answers a real request. A verdict comes back within the timeout. With the local machine off, the next step answers with the notice.

See `docs/plan/SPEC-router-and-admin.md`, Part 1, "Proof", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
