# Project scripts

Folder: `tools`

Written in Go and run with `go run`, so the same commands work on Windows, macOS and Linux. `rebuild`, `verify`, `doctor` and `snapshot` can be run twice and give the same result.

## rebuild

`go run ./tools/rebuild` takes a fresh checkout to a built, checked Keystone. Today it checks that Go is installed, builds `bin/keystone`, runs it once, starts it on a free port and fetches the embedded page over HTTP, opens the data folder and applies every tenant's migrations, runs `verify`, and prints the snapshot hash. Loading the test tenant is added by the item that builds it (P3-09). The first build on a machine downloads `modernc.org/sqlite`; after that everything runs offline.

## tenant

`go run ./tools/tenant add "Name"` adds a tenant to the data folder and prints its id. `go run ./tools/tenant list` lists the tenants. `--data <folder>` names another data folder; without it the `data.dir` setting is used. It is a thin wrapper over `internal/tenant` ([tenants.md](tenants.md)).

`go run ./tools/tenant password <tenant-id>` gives a tenant a password, or replaces the one it has, and ends every session that tenant had. It is how a person gets a password before any sign-in screen exists. The password is read from standard input, one line, and never from an argument, so it is not left in the shell's history. Typed at the prompt, it shows on the screen. It uses the shipped sign-in settings and `internal/signin` ([signin.md](signin.md)).

`go run ./tools/tenant secret set | list | delete <tenant-id> [name]` stores, lists and removes a tenant's secrets. A value is read from standard input and never printed. `--keeper file` chooses the key file for a new install on a machine with no key store ([secrets.md](secrets.md)).

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
| offline | Only the listed folders import a network package, and the tests pass with no keys and no way out to the network. |
| tenant isolation | Two tenants are made in a throwaway data folder, and every way one could reach the other is tried: rows and files through the other's store handle, a handle opened by a path, and a session used on the other tenant, re-addressed to it, or with its row copied across. Each must be refused. The code is in `tools/verify/isolation.go`, and an item that adds a way to reach tenant data adds its tries there. |
| fixed rules | No setting can switch off any of the four rules in the plan. See below. |
| no keys in output | An invented value is stored as a tenant's secret, and then looked for in everything Keystone returned, printed or wrote while it held it. It may come back only from the call that asks for it by name. The code is in `tools/verify/nokeys.go`, and an item that adds a place a key could reach adds a line there. See [secrets.md](secrets.md). |

The plan's other standing checks (context fit, the approval rule, the spend cap, recall, and the rest) are added to this list by the items that build what they check.

### What offline checks today

The plan's rule is that `verify` passes with no network and no keys, using the fake provider. Two things can be proven now, and both are in `tools/verify/offline.go`.

- **Who may use the network.** The check reads the imports of every Go file, tests included. A file that imports `net`, anything under `net/` except `net/url` and `net/netip`, `crypto/tls` or `golang.org/x/net` must sit in a folder on the list in `offline.go`. Today the list is `cmd/keystone`, because the server listens, `internal/audit`, because it wraps the server's handlers and opens no connection, and `tools/rebuild`, because it fetches the page from the binary it has just started. Everything else, the seam and the fake provider included, is proven to import no network package. An item that builds a part which talks to the network adds its folder to the list with the reason: the three adapters, the API, connectors, web search. A folder on the list that no longer imports a network package fails the check until it is taken off.
- **The tests need no key and no network.** The check runs `go test ./...` a second time with a changed environment. Every variable whose name ends in `_KEY`, `_TOKEN`, `_SECRET` or `_PASSWORD` is removed. `GOPROXY` is `off`, so Go cannot download a module. `HTTP_PROXY` and `HTTPS_PROXY` point at a port on this machine where nothing listens, so a call that leaves the machine through Go's standard HTTP client fails at once, while a test server on this machine still answers. A test that needs a key or the internet passes the `tests` check on a machine that has them and fails here.

What it does not prove yet: that a whole turn runs offline through the fake provider. There is no turn to run until P4-03. The test tenant (P3-09) and the turn API add that, and the adapters add their folders to the list.

### The fixed rules check

The code is in `tools/verify/fixedrules.go`. It proves four things on every run.

1. `settings.FixedRules()` holds the plan's four rules, word for word and in order. Changing a rule means changing `PLAN.md` and the code together.
2. No setting in the registry has a key under `rules.`, the part of the key space kept for the rules.
3. No override path stores a value for a rule. The check makes a tenant in a temporary folder and tries each rule as a person, as the admin, and as a row written straight into the tenant's database. Each must be refused.
4. The packages that enforce a rule cannot read a setting. `fixedRuleGuards` lists them for each rule. The check runs `go list -deps` on each one and fails if the settings registry or the tenant settings is among what it imports, itself or through another package.

Today `internal/tenant` guards tenant isolation and `internal/audit` guards call recording. The sensitive content rule is guarded by `internal/models`. The approval rule has no code yet. The item that builds each one adds its package to `fixedRuleGuards`, and adds a step to the check if the rule needs a proof of its own. Code that enforces a rule and code that reads settings therefore live in separate packages.

Since P3-02, `internal/models` guards the sensitive content rule: the registry's classes are what the rule reads ([models.md](models.md)). Routing (P3-08) and recall (P6-05) add their own packages. The `no keys in output` check also has a surface for the model registry, in `tools/verify/models.go`: a key reaches an adapter's builder from the vault, and is looked for in every error, in the registry as listed, and in the data folder.

## doctor

`go run ./tools/doctor` prints the state lines, the Go version, whether the build is healthy, how many items are done, and the next item each lane can pick up. An agent runs it at the start of every loop. It exits with an error when the build or the plan is broken, and fixing that is then the loop's item.

## snapshot

`go run ./tools/snapshot` prints one hash for the source tree and the number of files in it. Build output, the data folder and git's own folder are left out, and line endings are ignored, so a Windows checkout and a Linux one agree.
