# The profile and the map

Folder: `internal/knowledge/personmap`

Two short texts about a person start every call to a model. The **profile** is a few facts and preferences in the person's own words. The **map** is an outline of what their knowledge store holds: each space, the kinds of page in it, and a few titles. The map tells a model what there is to look up. It does not answer anything by itself. Recall (P6-05) does that.

Both are kept in the tenant's own database and reached through the tenant's store handle. The package opens no connection and reads no setting. The knowledge store itself is described in [knowledge.md](knowledge.md).

Decisions: [0180](decisions/0180-the-profile-is-one-text.md) for the profile, [0181](decisions/0181-the-map-has-two-versions.md) for the two versions of the map, [0182](decisions/0182-the-map-is-kept-and-keyed.md) for when it is built again, and [0183](decisions/0183-the-map-gives-up-detail-in-a-fixed-order.md) for how it is kept inside its limit.

## The profile

One text for each tenant. `ReadProfile` returns it and `SetProfile` replaces it. Space around the text is dropped, line ends are made plain, and an empty text clears it. A text longer than the limit passed in is refused with `ErrTooLong`, and the old one stays.

The profile goes to every model the person uses, hosted ones too. It is not in a space and has no label. What belongs in a sensitive space does not belong in the profile, and the screen that edits it should say so.

No route or screen edits it yet.

## The map

The map is made from `Store.Catalog`. It has one line for each space:

    - garden, 3 pages: plant 2 (Runner beans; Tomatoes); place 1 (North bed)
    - home, 4 pages: account 2 (Electricity account; Water account); other 1 (Bin days); project 1 (Shed roof)

Spaces are in order of name. Within a space the kinds are in order of how many pages they have, and a page whose kind is empty counts under `other`. The titles are the first few of each kind in order of path. A name from the store is put on one line and cut to a longest length, so no title can break the layout or fill the map alone.

The same catalog and limits always give the same text. Nothing in it is a date or a count of anything but pages, so the map can sit in the part of a call a provider caches.

The map carries no heading. Whoever places it in a call says what it is.

### Two versions, and fixed rule 3

Fixed rule 3 says content from a sensitive space reaches only models the registry marks local or trusted. A space's name, its kinds and its titles are content. So the map is made in two versions from one catalog:

- The **whole** version names every space, and marks a sensitive one: `- health (sensitive), 2 pages: ...`.
- The **open** version leaves every sensitive space out: no name, no kind, no title and no count of pages. It ends with one line that says how many spaces were held back: `- 1 sensitive space is not shown to this model`.

`Map.Text(mayReadSensitive bool)` is the only way to either text. The caller passes `Model.MayReadSensitive()` from the model registry ([models.md](models.md)). There is no other door: the two texts are not exported.

A space is sensitive when any entry in it has a label that is not `normal`. A blank label and a label the code has never seen read as sensitive, as decision [0031](decisions/0031-label-reads-as-sensitive.md) says.

The package is a guard of fixed rule 3 in `tools/verify/fixedrules.go`, so `verify` fails if it ever imports the settings registry or the tenant overrides. Its sizes come in as arguments.

`Map.Withheld` and `Prefix.WithheldSpaces` say how many spaces were left out, for the turn inspector. `Map.Spaces` and `Map.Pages` count the whole catalog, sensitive spaces too. They are for a screen the person sees and never for a model.

### Staying inside the limit

The map is held to a number of tokens, estimated as the budget does for plain text: characters over `budget.chars_per_token`, rounded up, with no margin. When the whole outline does not fit, detail is given up in a fixed order:

1. Fewer titles under each kind, down to none.
2. Fewer kinds in each space, keeping the ones with the most pages. The line ends `; and 3 more kinds`.
3. Fewer spaces, keeping the ones with the most pages. A last line says `- and 4 more spaces`.

If not even that fits, the map is empty. Each version is fitted by itself, so the open version may show more detail than the whole one.

### When it is built again

`Load` keeps the map in the tenant's database with a key that says which state of the catalog it was built from.

- A store with the `CatalogMarker` capability is asked for its marker. While the marker is the one the map was built from, the kept map is returned and the catalog is not read.
- A store without it has its catalog read and hashed each time, labels included, and the kept map is used while the hash is the same.
- A change of limits builds the map again.

A marker changes when a page is added, removed, renamed or moved, or a space's label changes. That is the store's promise in the contract suite, and the kept open version rests on it.

### With no store, and with a store that is down

With the `none` adapter, or with no store at all, the catalog is empty, the map is empty and nothing fails. A limit of 0 tokens means no map.

When the store cannot be read, the map is empty for every model, `StoreErr` says why, and no error is returned, so a chat carries on. The kept map is not used then: a space may have been made sensitive since it was built, and the label is read fresh or not at all. The kept map stays in the database for when the store is back.

## For the context builder

`ForContext` is the one call for the slot at the start of a call:

    p, err := personmap.ForContext(ctx, tenantStore, knowledgeStore, model.MayReadSensitive(), limits)

It returns a `Prefix`:

| Field | What it holds |
| --- | --- |
| `Profile`, `ProfileTokens` | The profile, cut to its limit, and its estimate in tokens. |
| `Map`, `MapTokens` | The map for this model and its estimate. Never over the limit. |
| `WithheldSpaces` | How many sensitive spaces were left out for this model. |
| `MapBuilt` | When the map was made. |
| `StoreErr` | Why the map is empty, when the knowledge store could not be read. |

An error is the tenant's own database failing, limits out of range, or a context that is done. `knowledgeStore` may be nil.

The text depends on the model's answer, so a chat that moves between a hosted and a local model has two starts, and each is cached by its own provider.

## Settings

The caller reads these and fills `Limits`. The package exports each key as a constant.

| Setting | Default | What it sets |
| --- | --- | --- |
| `knowledge.profile_max_chars` | 2000 | The longest profile, in characters, from 1 to 20000. For the whole install. |
| `knowledge.map_max_tokens` | 600 | The most tokens the map may be, from 0 to 20000. 0 sends no map. |
| `knowledge.map_titles_per_kind` | 3 | The most titles under one kind, from 0 to 20. |
| `knowledge.map_title_max_chars` | 60 | The longest a space's name, a kind or a title is shown, from 8 to 200. For the whole install. |
| `budget.chars_per_token` | 3 | The budget's own setting, in `settings/models.json`, so the map is measured the way a call is. |

`CheckLimits` refuses a value out of range with `ErrLimits` and names the setting.

## The tables

`person_profile` and `person_map`, each with one row at most, from the migration `20261125T060000_person_profile_and_map.sql`. `person_map` holds both versions, the key, the limits it was built with, the counts and when it was built.

## Checked by

`go test ./internal/knowledge/personmap`:

- Against the built-in store with four invented spaces, the map names every space, every kind and every title, and is at or under the default limit.
- Nothing from a sensitive space is in the open version, at any limit tried, and a space made sensitive leaves it at the next call.
- A blank label, an unknown label, and one unlabelled entry in a labelled space each make the space sensitive.
- A catalog of thirty spaces stays under limits from 1 token to 20000, at three sizes of token, and says what it left out.
- The kept map is used while the marker is the same and built again after a page is added, a page is removed, a label changes and the limits change.
- A store with no marker is compared by its catalog.
- `none`, a nil store, an empty store and a limit of 0 give an empty map. A store that is down gives an empty map and no error, and the kept map is still there afterwards.
- The profile is set, read, replaced, cleared, refused when too long, cut for a call, and is one tenant's own.
- The defaults in the registry pass `CheckLimits`.

## Not here yet

- The turn calls `ForContext` for each model call (`internal/turn/person.go`). Since P6-05 it passes the answer for the least of the call's route, not the first model's, so a local model with a hosted fallback behind it is given the open map ([knowledge-recall.md](knowledge-recall.md), decision [0210](decisions/0210-recall-searches-once-before-the-route.md)).
- A route and a screen for the profile (P6-07, P11-01).
- Facts the memory pass might offer for the profile (P6-06). Today only the person writes it.
- The Waymarks adapter has been run against a fake only, so the map has not been built from a real Waymarks.
- The estimate has no margin. A provider that counts tokens for itself may count the map a little over its limit. The budget's check on the whole call still holds.
