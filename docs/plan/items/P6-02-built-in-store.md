# P6-02. Built-in knowledge store

Status: doing (knowledge)
Lane: knowledge
Depends on: P6-01
Owner gate: none
Scope: Markdown folder plus a SQLite full-text index inside the tenant folder.
Proof: Passes the contract suite.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

What will change:

- A new package, `internal/knowledge/builtin`, registered as `built-in`. It implements `knowledge.Store` on tables in the tenant's own database, reached only through the `*tenant.Store` handle it is given.
- One migration, `20261124T230000_knowledge_builtin.sql`: spaces and their labels, pages, sources, the sources a page cites, page vectors, a change counter, and two FTS5 indexes.
- Pages are Markdown text. They are kept in the database and not as files, because the tenant handle has no `knowledge/` folder yet and `internal/tenant` belongs to the core lane. The folder half of the scope is the part left open, with a question for the owner.
- Labels are read from the spaces table in the same query as the content, on every call. A space with no row, or any label but `normal`, reads as sensitive.
- The store claims all four capabilities. Vectors are compared in Go, by cosine, against the stored vectors of the same model and size. No embedding model exists until P6-08, so this is proven with the suite's test vectors only.
- What the interface has no call for, writing a page, removing one, adding a source as text and labelling a space, are plain functions in the package. The suite's `Seed` and the test tenant's loader use them.
- `built-in` joins the `options` of `knowledge.adapter`, with settings for the default search limit, the snippet length and the size of one list of pages needing a vector.

How it will be proven:

- The contract suite in `internal/knowledge/knowledgetest` passes with every capability on.
- Tests of the package's own: a space with no label row reads as sensitive everywhere, two tenants do not see each other's pages, every option of the setting opens, the store refuses a nil tenant, and the writing functions refuse what they should.
- `go run ./tools/verify` is green.

