# P7-06. Search providers and the fallback

Status: todo
Lane: agents
Depends on: P2-03, P2-04
Owner gate: None.
Built without the gate: Everything.
Scope: Provider entries for a person and for the install, the install vault in `data/admin/`, moving `provider.tavily` to a first own provider, the five states and what moves a provider between them, what is left from three sources, the three strategies, at most `web.max_attempts` providers a search, soft tests with the growing wait and the sweep, the audit entries and monthly counts, and the `web.` settings. Tavily keeps working through it. The install vault touches `internal/secrets`, the core lane's: say so in the journal.
Proof: Every line of the spec's proof except the other providers and the screens, on stand-ins on this machine.

See `docs/plan/SPEC-web-search.md`, "Providers" to "Soft tests", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
