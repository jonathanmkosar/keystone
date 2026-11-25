# The built-in knowledge store

Folder: `internal/knowledge/builtin`

The built-in store is the knowledge store Keystone keeps by itself, for a person who wants memory and runs no other store. It is the adapter named `built-in`, one of the values of the `knowledge.adapter` setting. The interface it implements, and the contract suite it passes, are in [knowledge.md](knowledge.md).

Decisions: [0060](decisions/0060-built-in-store-in-the-tenant-database.md) for where the content is kept, [0061](decisions/0061-built-in-search-any-word-and-cosine.md) for how a search matches and scores, and [0062](decisions/0062-built-in-vectors-any-model.md) for vectors.

## Where it keeps things

Everything is in the tenant's own database, `keystone.db`, in tables whose names start with `knowledge_`. The adapter is given the tenant's store handle when it is opened and reaches the database only through it, so a store opened for one person cannot name another's. The tables are there for every tenant and are empty for a tenant who chose another store, or none.

| Table | Holds |
| --- | --- |
| `knowledge_spaces` | Each space and its label, `normal` or `sensitive`. |
| `knowledge_pages` | Each page: space, path, title, kind, the body as Markdown, and a revision number. |
| `knowledge_sources` | Each source: space, name, origin, date and text. A source is never changed. |
| `knowledge_page_sources` | The sources a page cites, in order. |
| `knowledge_page_vectors` | One vector for each page, with the model that made it and the revision it was made from. |
| `knowledge_meta` | A count of the changes to the catalog. |
| `knowledge_page_search`, `knowledge_source_search` | The full-text index, SQLite's FTS5, with stemming. |

The migration is `internal/tenant/migrations/20261124T230000_knowledge_builtin.sql`.

The plan describes the built-in store as a Markdown folder with an index. The pages here are Markdown, and they are rows and not files. Decision 0060 says why, and what is left open.

## Capabilities

The store claims all four: `AddSource`, `CatalogMarker`, `VectorSearch` and `PageVectors`.

## Labels

A space's label is read from `knowledge_spaces` in the same statement as the content, on every call. The store holds no content and no labels between calls, so a space made sensitive is sensitive on the next read, whichever handle reads it.

Only the exact text `normal` reads as normal. A space with no row in the table reads as sensitive, and so would any other text. The other tables do not reference `knowledge_spaces`, so a lost row cannot hide or remove content: it can only make it sensitive.

A space that does not exist yet is made, as a normal space, when the first page or source is written into it.

## Ids and revisions

A page's id is `p` and its row number, such as `p12`. A source's id is `s` and its row number. One is never taken for the other. Row numbers are never used twice, so the id of a removed page does not come back as another page.

A page is named by its space and path. Writing a page at a space and path that is already there replaces it and keeps its id. The revision is a number that goes up by one when the title, the kind, the body or the cited sources change. Writing the same page again changes nothing, so its vector stays good.

## Search

`Search` takes plain words, a vector, or both.

- **Words.** A page or a source is found when it holds any word of the query, in its title or name or in its text. Words are stemmed, so `egg` finds `eggs`. Nothing in the query is read as search syntax. The score is SQLite's own ranking (bm25), turned so that higher is better: a thing that holds more of the query's words, and rarer ones, scores higher. It is zero or more, with no upper end.
- **A vector.** A page is found when its stored vector is of the query's model and size and the cosine between the two is above zero. The score is that cosine. Sources have no vectors.
- **Both.** A page found both ways scores the sum.

A hit's snippet is the part of the text around the match, or the start of the page for a page only the vector found. Its length is the `knowledge.builtin_snippet_words` setting.

A search with no limit returns at most `knowledge.builtin_search_max_results` hits.

Vectors are compared in Go, one by one. A person's store is small enough for that.

## Vectors

`PutVector` stores one vector for a page, replacing the one before. It takes any model name and any size. It refuses a vector with no model, no numbers, a number that is not finite, or nothing but zeros (`ErrInvalid`), a page that is not there (`ErrNotFound`), and a revision that is not the page's own (`ErrStale`).

`VectorsNeeded` lists the pages with no vector, and the pages whose vector was made from an earlier revision, marked stale. A stale vector is still searched until a new one replaces it.

## The catalog marker

The marker is the count of changes to the catalog, as text. It moves when a page is added or removed, when a page's title or kind changes, and when a space's label changes. It does not move when only a page's body changes, or when a source is added.

## Writing

The `Store` interface writes one thing, a source of facts. `AddSource` turns the facts into a Markdown list, one fact to a line, and stores that as the source's text.

Everything else is a plain function that takes the tenant's handle:

| Function | What it does |
| --- | --- |
| `WritePage` | Adds a page, or replaces the one at that space and path. |
| `RemovePage` | Takes a page out, with its vector. The sources it cited stay. |
| `AddText` | Stores a source as it stands: a letter, a note, a file's text. |
| `SetLabel` | Marks a space `normal` or `sensitive`. |

A space's name has no slash, and a page's path is words joined by `/` with no `.` or `..` part, so a store can always be written out as folders of files.

These are for the test tenant's loader (P3-09), an import, the wiki upkeep jobs (P8-06) and the memory screens (P6-07). No route calls them yet.

## Settings

In `settings/knowledge.json`, read through the tenant's own values when a store is opened:

| Setting | Default | What it sets |
| --- | --- | --- |
| `knowledge.builtin_search_max_results` | 20 | The most hits for a search that names no limit. |
| `knowledge.builtin_snippet_words` | 24 | About how many words a snippet shows, from 1 to 64. |
| `knowledge.builtin_vectors_needed_max` | 100 | The most pages one call to `VectorsNeeded` lists when it names no limit. |

A value out of range stops the store from opening, with a message that names the setting.

## Errors

A call whose context is done returns the context's error. Any other failure of the database is wrapped as `ErrUnavailable`, so a chat carries on without the store.

`Close` lets go of nothing. The tenant's handle belongs to whoever opened it.

## Checked by

`go test ./internal/knowledge/builtin`:

- The contract suite passes, with all twelve checks run and none skipped.
- A space whose label row is gone, or holds text that is not a label, reads as sensitive in the catalog, a page, a source, every kind of search and the list of pages needing a vector.
- Two tenants on one install do not see each other's pages, sources or vectors.
- Every option of `knowledge.adapter` opens for a tenant, and every registered adapter is an option.
- The store does not open with no tenant, and closing it leaves the tenant's database open.
- A page written twice keeps its revision, a changed page keeps its id, and a removed page is gone from the catalog, the search and the vector list.
- A whole question finds a page that holds some of its words, a word finds its other forms, and search syntax in a query is plain text.
- A vector is compared only with vectors of its own model and size.

## Not here yet

- The Markdown folder. See decision 0060.
- The server does not import this package yet, so the binary cannot open `built-in`. The line is `_ "github.com/jonathanmkosar/keystone/internal/knowledge/builtin"` where the server is put together, in `cmd/keystone`, which belongs to the core lane.
- A real embedding model (P6-08). Vector search is proven with the suite's four-number vectors. When the embedding model is chosen, `VectorsNeeded` should also list a page whose vector came from another model. See decision 0062.
- Tuning. How a word score and a cosine weigh against each other, and recall's relevance threshold for this store, wait for recall on the test tenant (P6-05).
- Correcting and forgetting (P6-07), and moving a page to another path.
