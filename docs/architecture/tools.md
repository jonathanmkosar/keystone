# Project scripts

Folder: `tools`

Written in Go and run with `go run`, so the same commands work on Windows, macOS and Linux. `rebuild`, `verify`, `doctor` and `snapshot` can be run twice and give the same result.

## rebuild

`go run ./tools/rebuild` takes a fresh checkout to a built, checked Keystone. Today it checks that Go is installed, builds `bin/keystone`, runs it once, starts it on a free port and fetches the embedded page over HTTP, opens the data folder and applies every tenant's migrations, runs `verify`, and prints the snapshot hash. Loading the test tenant is added by the item that builds it (P3-09). The first build on a machine downloads `modernc.org/sqlite`; after that everything runs offline.

## tenant

`go run ./tools/tenant add "Name"` adds a tenant to the data folder and prints its id. `go run ./tools/tenant list` lists the tenants. `--data <folder>` names another data folder; without it the `data.dir` setting is used. It is a thin wrapper over `internal/tenant` ([tenants.md](tenants.md)).

## verify

`go run ./tools/verify` runs the standing checks. It needs no network and no keys. Nothing merges to `main` while it is red, and a check is never weakened to get to green.

| Check | What it proves |
| --- | --- |
| build | `go build ./...` works. |
| vet | `go vet ./...` finds nothing. |
| tests | `go test ./...` passes. |
| settings registry | The registry loads and every setting is valid. |
| plan items | Every work item has a status and a lane, every dependency exists, and there is no loop. |
| state | `STATE.md` says the phase, what is next, and what is blocked. |
| docs | Every folder with Go code has a doc, the plan files are present, and every decision record is linked from the overview. |
| no secrets | Nothing in the repo is shaped like a real key. |
| fixed rules | No setting can switch off any of the four rules in the plan. See below. |

The plan's other standing checks (tenant isolation, context fit, the approval rule, the spend cap, recall, and the rest) are added to this list by the items that build what they check.

### The fixed rules check

The code is in `tools/verify/fixedrules.go`. It proves four things on every run.

1. `settings.FixedRules()` holds the plan's four rules, word for word and in order. Changing a rule means changing `PLAN.md` and the code together.
2. No setting in the registry has a key under `rules.`, the part of the key space kept for the rules.
3. No override path stores a value for a rule. The check makes a tenant in a temporary folder and tries each rule as a person, as the admin, and as a row written straight into the tenant's database. Each must be refused.
4. The packages that enforce a rule cannot read a setting. `fixedRuleGuards` lists them for each rule. The check runs `go list -deps` on each one and fails if the settings registry or the tenant settings is among what it imports, itself or through another package.

Today `internal/tenant` guards tenant isolation and `internal/audit` guards call recording. The approval rule and the sensitive content rule have no code yet. The item that builds each one adds its package to `fixedRuleGuards`, and adds a step to the check if the rule needs a proof of its own. Code that enforces a rule and code that reads settings therefore live in separate packages.

## doctor

`go run ./tools/doctor` prints the state lines, the Go version, whether the build is healthy, how many items are done, and the next item each lane can pick up. An agent runs it at the start of every loop. It exits with an error when the build or the plan is broken, and fixing that is then the loop's item.

## snapshot

`go run ./tools/snapshot` prints one hash for the source tree and the number of files in it. Build output, the data folder and git's own folder are left out, and line endings are ignored, so a Windows checkout and a Linux one agree.
