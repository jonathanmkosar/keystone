# Knowledge store

Folder: `internal/knowledge`

The knowledge store is the long-term memory a person chooses. Waymarks is one, the built-in store is another, and a person may have none. This part is the door to it: one interface, `Store`, and plain data types. Everything outside the adapters speaks these and never a store's own format.

Today the folder holds the interface, the `none` adapter, the registry of adapters, and the contract suite. The built-in store is in `internal/knowledge/builtin` and has its own page, [knowledge-builtin.md](knowledge-builtin.md). The Waymarks adapter is in `internal/knowledge/waymarks` and has its own page, [knowledge-waymarks.md](knowledge-waymarks.md). The person's profile and the map of their store are in `internal/knowledge/personmap`, on the page [knowledge-profile-and-map.md](knowledge-profile-and-map.md). Recall is in `internal/knowledge/recall` ([knowledge-recall.md](knowledge-recall.md)), and the memory pass, which adds the facts of each turn as a source, is in `internal/knowledge/memory` ([memory-pass.md](memory-pass.md)).

Decisions: [0030](decisions/0030-knowledge-store-interface.md) for the shape of the interface and what having no store means, and [0031](decisions/0031-label-reads-as-sensitive.md) for the label.

## The calls

| Call | What it does | Needs |
| --- | --- | --- |
| `Capabilities` | Says which optional calls work. | |
| `Catalog` | Lists every page, ordered by space and then path. | |
| `Search` | Finds pages and sources, best first. | A query vector needs `VectorSearch`. |
| `ReadPage` | One page by id: its body, its revision, and the ids of the sources it cites. | |
| `ReadSource` | One source by id, with its text. | |
| `AddSource` | Stores a new source made of facts, and returns it as stored. | `AddSource` |
| `CatalogMarker` | A short text that stays the same while the catalog does. | `CatalogMarker` |
| `VectorsNeeded` | Lists the pages with no vector, or a stale one. | `PageVectors` |
| `PutVector` | Stores the vector made from one revision of a page. | `PageVectors` |
| `RemoveSource` | Takes a source out for good; a page that cited it no longer does. | `RemoveSource` |
| `Close` | Lets go of the handle. | |

Every call but `Capabilities` and `Close` takes a context, and a supported call whose context is done returns the context's error.

A handle is opened for one tenant. Nothing in the interface names a tenant, so a handle cannot reach another person's store.

## What the types carry

- An `Entry` is one page in the catalog: id, space, label, path, title, and kind. The kind is what the page is about, such as project or account, in the store's own words.
- A `Page` is an entry plus the body, the revision, and the ids of the sources it cites.
- A `Source` is something brought in and kept unchanged: id, space, label, name, origin, date and text.
- A `Fact` is one durable statement. A `NewSource` is a space, a name, an origin, a date and one or more facts. The origin is where the facts came from. For the memory pass it points back to the chat.
- A `Hit` is a page or a source that a search found: its type, id, space, label, title, a snippet and a score.

An id is text and means something only to the store that gave it. A page id and a source id may look the same, so read a hit with the call that matches its type.

A score is higher for a better match. Its scale is the store's own, so recall's relevance threshold is set for each adapter.

## Space and label

Every entry, page, source, hit and page need carries its space and that space's label, `normal` or `sensitive`. Recall and embeddings read the label to keep fixed rule 3: content from a sensitive space reaches only models the registry marks local or trusted.

`Label.Sensitive()` is true for every label but `normal`. A label an adapter left blank reads as sensitive, and so does the zero value of each type. An adapter that cannot tell says sensitive.

An adapter reads the label fresh. If a space is made sensitive after the handle was opened, the next read says so.

## Capabilities

| Flag | What the store can do | Asked by |
| --- | --- | --- |
| `AddSource` | Take a new source of facts. | The memory pass (P6-06). With the flag off it has nowhere to put a fact. |
| `CatalogMarker` | Say cheaply whether the catalog changed. | The person map (P6-04), to know when to rebuild. With the flag off it can read the catalog and compare. |
| `VectorSearch` | Search with a query vector as well as words. | Recall (P6-08). With the flag off a search is by words alone, and no query vector need be made. |
| `PageVectors` | List the pages that need a vector, and take one. | The embedding job (P6-08). |
| `RemoveSource` | Remove a source for good. | The Memory screen (P6-07), to forget a fact. With the flag off, Keystone withdraws the source instead ([memory-screens.md](memory-screens.md)). The built-in store has it; Waymarks does not, because only its owner erases a source. |

A caller asks the flags and never the adapter's type. A call whose flag is off returns `ErrNotSupported`.

The flags are fixed while a handle is open.

The marker changes when a page is added, removed, renamed or moved, or when a space's label changes. It may also change when nothing in the catalog did.

A vector carries the name of the embedding model that made it. A store may refuse a model or a size it is not set up for.

## Errors

| Error | Meaning |
| --- | --- |
| `ErrNotFound` | No page or source has that id. |
| `ErrNotSupported` | The store lacks the capability. |
| `ErrInvalid` | The call is wrong: a source with no facts, a vector the store cannot take. |
| `ErrStale` | The vector was made from a revision that is no longer the page's own. Read the page again. |
| `ErrUnavailable` | The store could not be reached. A chat carries on without it. |

Adapters wrap them. Test with `errors.Is`.

## With no store

`none` is an adapter like any other. It behaves as an empty store with no capability:

- `Catalog` and `Search` return nothing, with no error.
- `ReadPage` and `ReadSource` return `ErrNotFound`.
- `AddSource`, `CatalogMarker`, `VectorsNeeded`, `PutVector`, and a search with a vector return `ErrNotSupported`.

A caller needs no special case for it. A search finds nothing, the catalog is empty, and the `AddSource` flag is off, which is the same answer any store gives that cannot take a source.

## Choosing an adapter

The `knowledge.adapter` setting, in `settings/knowledge.json`, names a person's adapter. Its default is `none`, and a person may change it for themselves.

`knowledge.Open(ctx, name, tenantStore)` opens the store. The name is a plain argument. Stored per-tenant values are in `internal/tenantsettings`. Whoever opens the store for a turn reads the tenant's value of `knowledge.SettingAdapter` through P2-04's lookup and passes it in.

`Open` refuses a name this build does not have, and says which names it has.

For a tenant, and any adapter but `none`, `Open` hands back the adapter's store with the tenant's withdrawn sources left out of searches, reads and page citations (P6-07, `withdrawn.go`, decision [0371](decisions/0371-with-waymarks-a-forgotten-source-is-withdrawn.md)). `Unwrap` gives an adapter's own tests their type back.

## How to write an adapter

1. Make a package under `internal/knowledge/`, such as `internal/knowledge/builtin`.
2. Write a type with every method of `knowledge.Store`. A method behind a capability the store lacks returns `knowledge.ErrNotSupported`. `AddSource` calls `knowledge.CheckNewSource` first.
3. Set the label on every value you return. Read the label from the store each time.
4. Register a constructor from an `init` function: `knowledge.Register("built-in", open)`. The constructor is given the tenant's handle, which is how it finds that tenant's database and folders. A tenant's settings and secrets arrive with P2-04 and P2-03, and an adapter is expected to read them through the same handle.
5. Add the name to the `options` of `knowledge.adapter` in `settings/knowledge.json`.
6. Import the package, for its registration, where the server is put together. The first adapter to do so adds a test there that every option of the setting opens.
7. Call the contract suite from the package's tests, and list the folder in `componentDocs` in `tools/verify/main.go`.

## The contract suite

`internal/knowledge/knowledgetest` holds the checks every adapter must pass. An adapter's test gives it a harness:

```go
func TestContract(t *testing.T) {
	knowledgetest.Run(t, knowledgetest.Harness{
		New:  func(t *testing.T) knowledge.Store { /* a fresh store */ },
		Seed: func(t *testing.T, s knowledge.Store, c knowledgetest.Content) { /* put c into s */ },
	})
}
```

- `New` returns a fresh store. It may hold what the store puts there by itself, and nothing else.
- `Seed` puts pages and sources into the store, and marks spaces sensitive, by whatever means the adapter has outside the interface. It adds to what is there. A page at a space and path that is already there replaces the old one.
- Vectors are of the model `knowledgetest.VectorModel` and the size `knowledgetest.VectorSize`. A store that checks either is set up to take them.

Run it with `go test ./internal/knowledge/...`. Each check is a subtest:

| Check | What it proves |
| --- | --- |
| capabilities | The flags do not change. A store with no `Seed` claims none. |
| fresh store | Reads are empty and an unknown id is `ErrNotFound`. |
| unsupported calls | Every call whose flag is off returns `ErrNotSupported`. |
| cancelled context | Every call stops. |
| catalog | Every page is listed once, in order, with its space, label, path, title and kind. |
| read page and source | A page reads as its catalog entry says, with its body, its revision, and the sources it cites. |
| search | Words find the pages and sources that hold them, best first, inside the limit and the space named. Hits from the sensitive space are labelled. |
| space made sensitive later | The next read of the catalog, a page, a source and a search all say sensitive. |
| catalog marker | Stays the same across reads. Changes when a page is added and when a space's label changes. |
| add source | A source with facts is returned, reads back the same, is found by search, and takes the label of its space. A source with no space, name or facts is refused. |
| vector search | The page nearest the query vector comes first. Words and a vector together find what either finds. |
| page vectors | Pages with no vector are listed. A stored vector takes a page off the list, a changed page comes back as stale, and a vector for an old revision is refused. |
| remove source | A removed source is not read or found, a page that cited it no longer does and moves on a revision, an unknown or removed id is `ErrNotFound`, and the other sources stay. |

A check behind a flag is skipped when the flag is off. A check that needs content is skipped when the harness has no `Seed`. Only a store that can hold nothing leaves `Seed` out, and the suite fails such a store if it claims a capability.

The suite's own tests run it against a small store held in memory, with each flag on and off. They also run it against copies of that store with one fault each, such as a blank label or a vector taken for an old revision, and check the suite fails each one. The memory store is in a test file. It is in no binary, it is not registered, and no tenant can choose it.

## How Waymarks fits

The interface was shaped against what Waymarks offers, so P6-03 is a translation and not a redesign.

| Here | In Waymarks |
| --- | --- |
| `Catalog` | Its catalog call, which lists every page the token may read. The label comes from its list of spaces. The kind is a page property. |
| `Search` | Its keyword search. With a vector, its search that takes words, a vector, or both. |
| `ReadPage` | A page by its number, with a revision number and the sources it cites. |
| `ReadSource` | A source's record and its text, which one call there returns together. |
| `AddSource` | Its source upload. The facts become the text and the file. |
| `CatalogMarker` | A hash of its catalog and of the labels of its spaces. Its change log cannot be used: decision [0091](decisions/0091-waymarks-marker-is-a-hash-of-the-catalog.md). |
| `VectorsNeeded`, `PutVector` | Its list of pages needing a vector, and its call that stores one for a page revision. It takes one model and one size, set in the instance. |

What shaped the interface:

- Waymarks numbers pages and sources apart, so an id belongs to one kind.
- Waymarks marks an answer that holds anything from a sensitive space. A token for every space does not reach a sensitive one until the space is named in its grant.
- Its grants split the work. Reading and searching is one grant. Adding sources is another, and that one reads nothing. Storing a vector counts as writing a page. That is why adding sources, searching with a vector and storing vectors are three flags: an adapter sets each from the tokens it was given.
- Waymarks keeps a source's text and has no field for facts, so a source read back has text and no list of facts.
- A fresh Waymarks holds a page of its own rules. The suite allows a fresh store to hold what the store itself put there.

## Checked by

`go test ./internal/knowledge/...`. The `none` adapter passes the contract suite. The suite passes a correct store and fails each faulty one. A label that is not `normal` reads as sensitive. The setting defaults to `none`, and `none` opens with no tenant, no folder and no network.

## Not here yet

- The server does not import an adapter's package yet, so the binary has `none` alone. The test that every option of the setting opens is in `internal/knowledge/waymarks`, the one package whose tests have every adapter in their build, until the server has a place for it.
- Stored per-tenant values of `knowledge.adapter` exist (`internal/tenantsettings`). A turn calls `Open` for the person map and for recall ([knowledge-recall.md](knowledge-recall.md)), and the memory pass opens it after the turn to add a source of facts when the store has the `AddSource` flag ([memory-pass.md](memory-pass.md)). With Waymarks that source goes out under the intake token.
- Roles. The plan gives Waymarks one token for each Keystone role. The interface has no role: the Waymarks adapter picks the token by the call, so reads go out under the reader token, `AddSource` under the intake token, and page vectors under a third, the embedder (decision [0090](decisions/0090-waymarks-three-tokens.md)). Chat is handed the whole store, and recall and the lookups only read (decision [0213](decisions/0213-the-lookups-are-plain-values.md)).
- Writing pages, which the wiki upkeep jobs do (P8-06), adds a flag and a call when its item is built. Correcting and forgetting a fact (P6-07) added `RemoveSource` ([memory-screens.md](memory-screens.md)).
