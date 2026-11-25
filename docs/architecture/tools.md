# Project scripts

Folder: `tools`

Written in Go and run with `go run`, so the same commands work on Windows, macOS and Linux. `rebuild`, `verify`, `doctor` and `snapshot` can be run twice and give the same result.

## rebuild

`go run ./tools/rebuild` takes a fresh checkout to a built, checked Keystone. Today it checks that Go is installed, builds `bin/keystone`, runs it once, starts it on a free port and fetches the embedded page over HTTP, opens the data folder and applies every tenant's migrations, runs `verify`, and prints the snapshot hash. Loading the test tenant is added by the item that builds it (P3-09). The first build on a machine downloads `modernc.org/sqlite`; after that everything runs offline.

## tenant

`go run ./tools/tenant add "Name"` adds a tenant to the data folder and prints its id. `go run ./tools/tenant list` lists the tenants. `--data <folder>` names another data folder; without it the `data.dir` setting is used. It is a thin wrapper over `internal/tenant` ([tenants.md](tenants.md)).

`go run ./tools/tenant password <tenant-id>` gives a tenant a password, or replaces the one it has, and ends every session that tenant had. It is how a person gets a password before any sign-in screen exists. The password is read from standard input, one line, and never from an argument, so it is not left in the shell's history. Typed at the prompt, it shows on the screen. It uses the shipped sign-in settings and `internal/signin` ([signin.md](signin.md)).

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
| tenant isolation | Two tenants are made in a throwaway data folder, and every way one could reach the other is tried: rows and files through the other's store handle, a handle opened by a path, and a session used on the other tenant, re-addressed to it, or with its row copied across. Each must be refused. The code is in `tools/verify/isolation.go`, and an item that adds a way to reach tenant data adds its tries there. |

The plan's other standing checks (tenant isolation, context fit, the approval rule, the spend cap, recall, and the rest) are added to this list by the items that build what they check.

## doctor

`go run ./tools/doctor` prints the state lines, the Go version, whether the build is healthy, how many items are done, and the next item each lane can pick up. An agent runs it at the start of every loop. It exits with an error when the build or the plan is broken, and fixing that is then the loop's item.

## snapshot

`go run ./tools/snapshot` prints one hash for the source tree and the number of files in it. Build output, the data folder and git's own folder are left out, and line endings are ignored, so a Windows checkout and a Linux one agree.
