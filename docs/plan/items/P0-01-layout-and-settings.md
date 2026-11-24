# P0-01. Repo layout and settings registry

Status: done
Lane: core
Depends on: nothing
Owner gate: none
Scope: The repo layout from PLAN.md. The settings registry: every setting with its default, description and whether a tenant may override it.
Proof: `go build ./...` works, `keystone settings` prints the registry, registry tests pass.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Plan: the layout from PLAN.md, a Go module with no outside dependencies, and a `settings` package that merges one JSON file per lane into a sorted registry. `keystone settings` prints it.

Result: built November 24, 2026.

- `settings/settings.go` reads and checks the lane files. `settings/core.json` and `settings/interface.json` hold the first three settings.
- "Whether a tenant may override it" became a scope with three values, because the spend cap is set per person by the admin. See decision 0001.
- `cmd/keystone` has `settings` (table, `--json`, `--lane`), `version` and `help`.
- Proof: `go build ./...` works, `keystone settings` prints 3 settings, and the registry tests pass (`TestRegistryLoadsAndIsSorted`, `TestLaneFilesAreMerged`, `TestReadRefusesBadFiles`, `TestValidate`, `TestScopeDecidesWhoMayChange`, `TestCheckValue`). A Windows build of the binary compiles. It has not been run on Windows yet: that is part of P0-03.
