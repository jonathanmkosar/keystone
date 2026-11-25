# P2-03. Per-tenant secrets

Status: doing (core)
Lane: core
Depends on: P2-01
Owner gate: none
Scope: A data key per tenant, wrapped by an install master key held by the OS (DPAPI on Windows, keychain elsewhere). secrets.bin holds provider keys and connector tokens.
Proof: secrets.bin cannot be read without the master key; the no-keys-in-output check is added to verify.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

