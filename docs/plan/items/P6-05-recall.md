# P6-05. Recall

Status: doing (knowledge)
Lane: knowledge
Depends on: P6-04, P5-01, P3-08, P3-09
Owner gate: none
Scope: Automatic search on every message with a relevance threshold; sensitive hits dropped for models not allowed them; the search-first rule; lookup tools.
Proof: The recall and sensitive-content checks pass on the test tenant with both stores.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

A new package, `internal/knowledge/recall`. It imports the knowledge interface and the tenant handle, and never `settings` or `internal/tenantsettings`: it is a guard of fixed rule 3, so every value comes in as an argument.

1. **Automatic recall.** `Find` searches the store with the person's newest message, by words, and keeps hits at or above a relevance threshold. The threshold is one setting for each adapter, because a score is on the store's own scale. Each kept hit's page or source is read, cut to a size bound. The result, a `Found`, holds everything and exports none of it.
2. **Fixed rule 3.** `Found.For(mayReadSensitive bool)` is the only way to the items. A sensitive hit is dropped for a model that may not read it, and a missing label reads as sensitive. The answer also says whether a sensitive item was kept. The turn passes that to routing, where it passes `false` today, so every model that may not read sensitive content leaves the route and the fallback chain. A chat that has been sent sensitive content stays held to such models.
3. **The search-first rule.** A setting holds the sentence. The turn adds it to the standing instructions, the builder's existing `rules` part, only when a store is open.
4. **Lookups.** `Lookups(mayReadSensitive, options)` returns three plain values: search, read a page, read a source. Each has a name, a description, a JSON schema, a read-only mark and a run function. No registry is built here: the tool loop (P7-01) wraps them.
5. **In the turn.** One new file in `internal/turn` opens the store, calls `Find` inside a timeout that is a setting, and fills the builder's `Recall` slot. A store that is down, slow or missing gives nothing and a line in the log. With `none` nothing is opened.
6. **What the inspector reads.** A record of what was found, kept and dropped, and why, in a new table beside the turn.

Proof: two new checks in `verify`, `recall` and `sensitive content`, run on the test tenant with the built-in store and with the Waymarks adapter against a fake Waymarks on this machine. A message about something in the store brings it into the call. A message about nothing in the store brings nothing. Content from a sensitive space never appears in a call to a model that may not read it, by recall, by a lookup or by fallback. The invented knowledge is a fixture under `fixtures/test-tenant/knowledge/`.

