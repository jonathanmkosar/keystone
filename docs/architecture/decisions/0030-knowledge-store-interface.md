# 0030. The knowledge store is one interface with capability flags, and having no store is an adapter

November 24, 2026 · decided in P6-01

## The question

The plan lets a person choose their long-term memory: Waymarks, the built-in store, or none. Recall, the person map, the memory pass and the embedding job all have to work with whichever was chosen. Three things had to be settled. Is there one interface or one for each kind of use? What does a caller do when a store cannot do something? And what does a caller do when there is no store?

## The decision

**One interface, `knowledge.Store`, with plain data types.** Catalog, search, read a page, read a source, add a source, a catalog marker, and the two calls for page vectors. A handle is opened for one tenant, and nothing in the interface names a tenant.

**What a store may lack is a flag.** `Capabilities` returns a struct of yes or no values: `AddSource`, `CatalogMarker`, `VectorSearch` and `PageVectors`. A call whose flag is off returns `ErrNotSupported`. A caller asks the flags and never the adapter's type.

**No store is the `none` adapter.** It behaves as an empty store with every flag off. It is the default of the `knowledge.adapter` setting.

**Adapters register by name.** An adapter registers a constructor from its `init` function, and `Open` takes the chosen name as a plain argument.

**Every adapter passes one contract suite.** It is in `internal/knowledge/knowledgetest`, and the suite's own tests prove it fails a store with a fault.

## Why

- With one interface, a new store is one package and one line in a setting. With an interface for each use, every new caller would touch every adapter.
- The flags follow how Waymarks grants access: reading, adding sources and storing vectors are separate grants. An adapter sets each flag from the tokens it holds, so the same adapter can be read-only for one person and not for another.
- A caller that checks for "no store" in one place forgets to in the next. With `none` there is nothing to check. A search finds nothing, and the `AddSource` flag is off, which any store may answer.
- `Open` takes a name and not a tenant's settings, so the package does not depend on how settings are stored.
- A contract in prose drifts. A suite that every adapter's tests call does not, and one that is itself tested against faulty stores is known to look at what it claims to.

## What it costs

- The interface is shaped against Waymarks. A store that works very differently may fit badly, and the built-in store (P6-02) is the first test of that.
- A new optional call means a new flag, a new check in the suite, and a look at every adapter.
- There is no role in the interface. Whether chat and sub-agents get a view that cannot add sources is left to P6-03 and P6-05.
