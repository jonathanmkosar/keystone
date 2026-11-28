# P7-07. Brave, Exa, Serper and SearXNG

Status: todo
Lane: agents
Depends on: P7-06
Owner gate: The owner's keys for Brave, Exa and Serper, and his SearXNG's address (live check only).
Built without the gate: All four adapters, proven on stand-ins.
Scope: One `Searcher` for each, with its request and its answer's shape checked against the provider's current documentation at the time it is built and recorded in a decision record; each one's refusals and limits told apart; Brave's quota headers read; SearXNG's endpoint checked as local or trusted.
Proof: Each adapter's request and its results on a stand-in; each refusal and limit moves the provider to the right state; the live check with the owner's keys and SearXNG.

See `docs/plan/SPEC-web-search.md`, "Providers" and "States", for the design this item delivers; `docs/plan/PLAN.md` for the intent; and `docs/plan/PARALLEL.md` for lanes and gates.
