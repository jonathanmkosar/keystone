# P6-04. Profile and person map

Status: provisional
Lane: knowledge
Depends on: P6-02
Owner gate: none
Scope: Profile facts and preferences; person map generated from the catalog and rebuilt on change; both in the cached prefix.
Proof: The map lists every space and kind of page in the test store and stays under its token limit.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result


### Plan, November 25, 2026

What will change:

- A new package, `internal/knowledge/personmap`, with no network import and no way to read a setting. Sizes come in as arguments.
- The profile: one short text for each tenant, kept in the tenant's own database, read with `Profile` and written with `SetProfile`, which refuses a text over the limit it is given. A route and a screen are later items.
- The map: a compact outline of the store, made from `Store.Catalog`. One line for each space, with its page count, its kinds of page with their counts, and a few titles under each kind. It is cut down in fixed steps until it fits its token limit: fewer titles, then fewer kinds, then fewer spaces, and it says what it left out.
- Two versions of the map are made from one catalog. The whole one is for a model that may read sensitive content. The other leaves out every sensitive space, by name, kind, title and count, and says only how many spaces were held back. `Map.Text(mayReadSensitive bool)` is the only way to the text. A missing label reads as sensitive. The package joins `fixedRuleGuards` for rule 3.
- The map is kept in the tenant's database with the catalog marker it was made from. It is rebuilt when the marker differs, where the store has one, and when a hash of the catalog differs where it has none. A change of limits rebuilds it too.
- When the store cannot be reached the map is empty, for both kinds of model, and the chat carries on.
- One function for the context builder's slot, `ForContext`, which returns the profile text, the map text and an estimate of each in tokens.
- One migration, `20261125T060000_person_profile_and_map.sql`, with two one-row tables.
- Settings in `settings/knowledge.json`, read by the caller: the profile's longest length, the map's token limit, titles for each kind, and a title's longest length.

How it will be proven, in `go test ./internal/knowledge/personmap`:

- Against the built-in store holding invented pages in several spaces: the map names every space and every kind, and its estimate is at or under the limit, at the default limit and at small ones.
- A sensitive space, and a space with a blank label, appear in the whole version and nowhere in the other, which carries only the count.
- The stored map is used again while the marker is the same, and rebuilt after a page is added, a page is removed, and a label changes.
- A store with no marker rebuilds when its catalog changes, and not otherwise.
- With `none` the map is empty and nothing fails. A store that is down gives an empty map and no error.
- The profile is stored, replaced, cleared, refused when too long, and is one tenant's own.

Not in this item: placing the two texts in a call (P5-01, the chats lane), a route or a screen for the profile (P6-07, P11-01), and facts the memory pass might add to the profile (P6-06).

### Result (November 25, 2026)

Provisional. It waits on one thing: the scope ends "both in the cached prefix", and nothing puts the two texts in a call until the context builder (P5-01) is merged and calls `personmap.ForContext`. The proof passes.

Built:

- `internal/knowledge/personmap`: the profile (`ReadProfile`, `SetProfile`), the map (`Build`, `Load`, `Map.Text`), and `ForContext` for the context builder's slot.
- The map in two versions for fixed rule 3. The one for a model that may not read sensitive content leaves every sensitive space out and says only how many were held back. The package is a guard of the rule in `verify`.
- The migration `20261125T060000_person_profile_and_map.sql`, and four settings in `settings/knowledge.json`.

Proven:

- Against the built-in store with four invented spaces, the map names every space and every kind of page and is under its default limit of 600 tokens. A catalog of thirty spaces stays under every limit tried, from 1 token to 20000.
- Nothing from a sensitive or unlabelled space is in the open version, and a space made sensitive leaves it at the next call.
- The map is kept while the catalog marker is the same and built again when it changes. A store with no marker is compared by its catalog. With `none` the map is empty and nothing fails.
- `go run ./tools/verify`: 15 of 15.

The doc is `docs/architecture/knowledge-profile-and-map.md`, the decisions are 0180 to 0183, and the journal entry is `docs/journal/2026-11-25-knowledge-004.md`, which has two questions for the owner and the call for the integrator.
