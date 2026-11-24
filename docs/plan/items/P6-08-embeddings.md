# P6-08. Embeddings

Status: todo
Lane: knowledge + models
Depends on: P3-02, P6-03, P6-05
Owner gate: none
Scope: Embedding as a model seam capability with its own registry entries, local first. Page vectors are pushed to stores that take them (Waymarks runs no embedding pipeline of its own). Recall gets a query vector, so search is by meaning as well as by keyword. Stale vectors are refreshed in the background. A sensitive page is embedded only by a local or trusted model.
Proof: Recall by keyword and meaning answers more of the test tenant's recall questions than keyword alone, and no sensitive page reaches a hosted embedding model.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

