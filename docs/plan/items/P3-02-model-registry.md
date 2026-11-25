# P3-02. Model registry

Status: done
Lane: models
Depends on: P3-01, P2-04
Owner gate: none
Scope: Per-tenant registry: endpoint, adapter, context window, output limit, capabilities, price, local, hosted or trusted.
Proof: Adding a model is a settings change; an invalid entry is refused with a reason.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What will change:

- A new package, `internal/models`: the registry. One entry per model a tenant can use: registry name, label, adapter, endpoint, the provider's own name for the model, the name of the secret that holds its key, context window, output limit, capabilities, price, and its class: `local`, `hosted` or `trusted`.
- `Check` refuses an entry that is not valid and says why. `Decode` reads one from JSON and refuses a field it does not know.
- Each tenant's entries are kept in a `models` table in its own database, one row per model, the entry as checked JSON. `Add`, `Replace`, `Remove`, `Get` and `List` take the tenant's store handle. An entry is checked again each time it is read.
- The class is what fixed rule 3 rests on, so the package is a guard for `rules.sensitive_content`: it imports neither the settings registry nor tenant settings, and takes its one limit as an argument. `MayReadSensitive` is the only place the class is turned into yes or no.
- A new package, `internal/adapters`: turns an entry into a `seam.Provider`. It looks up the adapter's builder and reads the key by name from the tenant's vault, never from settings or the environment. The fake is the only builder today. The three real adapters (P3-04 to P3-06) each add one.
- A setting, `models.max_per_tenant`, in `settings/models.json`.
- Example entries and refused entries in `fixtures/models/`, invented.

What is not in this item: routing and the fallback chain (P3-08), the budget (P3-07), the routes and the screen that edit the registry (P2-05 and P11-01), and a command that adds a model.

How it will be proven:

- Adding a model is a row in the tenant's table and no change to code: a test adds each example entry to a tenant, reads it back after the database is reopened, and gets a working provider for the fake one.
- Every refused entry in `fixtures/models/refused.json` is refused with the reason the file expects.
- One tenant's models are not another's.
- A key is read from the vault by name, a missing key is refused by name, and no error holds a key's value. The `no keys in output` check gets a line for this.
- `verify` is green, with `internal/models` listed as a guard in the `fixed rules` check.


### Result

Done on November 24, 2026.

- `internal/models` is the registry. An entry has a name, a label, an adapter, an endpoint, the provider's name for the model, the name of the secret that holds its key, a context window, an output limit, capabilities, a price and a class. Each tenant's entries are rows in its own `models` table (decision 0050).
- The proof holds. Adding a model is a row and no code: a test adds the six example entries to a tenant, reads them back after the database is reopened, and calls the fake one. An invalid entry is refused with a reason: each of the 25 entries in `fixtures/models/refused.json` is refused for the reason the file gives.
- The class is what fixed rule 3 reads, so the package is a guard for `rules.sensitive_content` and reads no setting (decision 0051). An entry marked `local` must have an address on this machine or the home network.
- `internal/adapters` opens a model as a `seam.Provider`, with the key read by name from the tenant's vault (decision 0052). The fake is the only adapter built. An entry may name one of the other three, and opening it says the adapter is not in this build.
- One setting, `models.max_per_tenant`. `verify` is 12 of 12, with a registry surface in `no keys in output`.
- Not here: a route, a screen or a command that adds a model, and anything that stops a call because of its class. Those are P2-05, P11-01 and P3-08.
- One question for the owner, which holds up nothing built: who may change a person's registry. Recommended: the admin only.

Journal: `docs/journal/2026-11-24-models-003.md`.
