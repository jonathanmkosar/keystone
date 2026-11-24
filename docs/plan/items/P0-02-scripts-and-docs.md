# P0-02. Project scripts and docs skeleton

Status: done
Lane: core
Depends on: P0-01
Owner gate: none
Scope: `rebuild`, `verify`, `doctor`, `snapshot` under tools/. Docs skeleton, architecture overview, journal folder, STATE.md kept current.
Proof: `go run ./tools/rebuild` then `go run ./tools/verify` is green on Windows; `doctor` prints phase, next item and build health.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan: the four scripts in Go under `tools/`, sharing `internal/project`, and a doc for each part with an overview.

Result: built November 24, 2026.

- `verify` runs 8 standing checks: build, vet, tests, settings registry, plan items, state, docs, no secrets. It fails when a folder holds Go code and has no doc.
- `doctor` prints the state, the build's health, item counts and the next ready item in each lane.
- `snapshot` hashes the tree, ignoring line endings and build output.
- `rebuild` builds `bin/keystone`, runs it, and runs verify and snapshot. The data folder and the test tenant are added by P2-01 and P3-09.
- Proof, on Linux with Go 1.24.7: `go run ./tools/rebuild` then `go run ./tools/verify` is green, 8 of 8. Rebuild run twice gave the same snapshot. A planted key and a broken dependency each turned verify red. Not yet run on Windows: that is P0-03's check.
