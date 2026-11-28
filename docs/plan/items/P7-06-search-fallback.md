# P7-06. Search providers and the fallback

Status: done
Lane: agents
Depends on: P2-03, P2-04
Owner gate: None.
Built without the gate: Everything.
Scope: Provider entries for a person and for the install, the install vault in `data/admin/`, moving `provider.tavily` to a first own provider, the five states and what moves a provider between them, what is left from three sources, the three strategies, at most `web.max_attempts` providers a search, soft tests with the growing wait and the sweep, the audit entries and monthly counts, and the `web.` settings. Tavily keeps working through it. The install vault touches `internal/secrets`, the core lane's: say so in the journal.
Proof: Every line of the spec's proof except the other providers and the screens, on stand-ins on this machine.

See `docs/plan/SPEC-web-search.md`, "Providers" to "Soft tests", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan

Branch `agents/p7-06-search-fallback`, cut from `reboot`. All new code is in new files of `internal/tools/web` (the agents lane's), so `web_search` keeps its name, its result form and its held-chat rule.

- **Entries.** `web.Provider` (name, kind, endpoint, key name, monthly and free quota, resets, cost per search, enabled), checked as the spec says. A kind is registered by its adapter (`tavily.go` today; P7-07 adds `brave.go`, `exa.go`, `serper.go`, `searxng.go`, each one `register` call), with its default endpoint, whether it needs a key, whether it is metered, and an optional entry check (SearXNG's local address). An adapter has one method, `Search(ctx, key, Query) (Answer, error)`; an answer can carry what the provider reported is left and when it resets; a failure is a `*web.Failure` saying why (quota, rate limit with its wait, unavailable, refused, malformed). An adapter with a free check also has `Check(ctx, key) (Quota, error)`; Tavily's is `GET /usage`.
- **Where they are kept.** A person's own: tables in their database (one tenant migration after 20261128T000000), keys in their vault as `search.<name>`. The install's: the same tables in a database of their own, `data/admin/search.db`, and keys in the install vault `data/admin/secrets.bin`. Two small new files outside the lane: `internal/secrets/install.go` (`OpenInstall`, bound to the id `install`, which no tenant id can be) and `internal/tenant/admin_folder.go` (`AdminFolder`, `InstallVault`, with the install's keeper). P15-01's `data/admin/admin.db` is not touched.
- **The move.** A person with `provider.tavily` and no move recorded gets a first own provider `tavily` whose key name stays `provider.tavily`, so nothing is copied and the owner's key works with no step from him; it happens at start (the sweep's first pass) and at a search, whichever comes first, once.
- **States and choosing**, as the spec says: ready, cooling, out, failing, refused, with the move rules, what is left from the provider, then Keystone's count, then unknown; `free_first`, `order` and `weighted` within the person's own providers and then within the install's; at most `web.max_attempts` tried; moving on for quota, rate limit, unavailable and refused, never for malformed; one line per provider when none answers, never a key.
- **Soft tests**: back at a known reset; a free check when the test is due, run by a sweep every `web.test_tick` (started with the server); without one, the next real search tries the provider first, alone, and goes on to the others if it fails; the wait from `web.test_first` (or `web.fail_cooldown` for failing) doubling to `web.test_max`; one test at a time per provider.
- **Records**: the tool's own audit entries hold the result, whose last line says which provider answered and why each tried before it was passed over (`web.Answered` reads it back for P7-08's step row); a count of searches and cost per provider per month.
- **Settings**: the eleven `web.` settings in `settings/agents.json`.
- **Surface for P7-08**: `web.Own(store)` and `web.OpenInstall(install)`, each with List (entry, state, what is left, searches and cost this month, whether a key is set, never a key), Add, Update, SetKey, Remove and Order. No routes here: the screens and their routes are P7-08.
- **Proof** (`go test ./internal/tools/web`, stand-ins on this machine only, a stand-in Tavily on `httptest`): every line of the spec's proof but the other providers and the screens, the move (a search still answers through Tavily after it), and a `no keys in output` surface for both vaults (`tools/verify/searchproviders.go`). Left: P7-07's adapters and live check, P7-08's screens and routes, a command-line way to add an install provider (with P7-08 or P15-02).

## Result

Built as planned, November 27, 2026, on `agents/p7-06-search-fallback`; see `docs/architecture/web-search.md` ("Search providers and the fallback"), decisions 0680 to 0686 and the journal `docs/journal/2026-11-27-agents-015.md`. Every line of the spec's proof but the other providers, the screens and the live check passes on stand-ins on this machine (`go test ./internal/tools/web`, `go test ./internal/tenant`, and a `search providers` surface in `no keys in output`). Two choices the spec left open: the moved Tavily entry keeps `provider.tavily` as its key's name (0681), and a person's own providers come before the install's whatever the strategy (0682). Left for P7-07: Brave, Exa, Serper and SearXNG adapters and the live check with the owner's keys; for P7-08: the routes and screens over `web.Own` and `web.OpenInstall`, and the step row over `web.Answered`.
