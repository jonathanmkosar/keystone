# P6-01. Knowledge store interface

Status: done
Lane: knowledge
Depends on: P2-01
Owner gate: none
Scope: Catalog, search, read page, read source, add source with facts; capability flags; per-tenant choice of adapter or none.
Proof: A contract test suite that every adapter must pass; Keystone runs with no store.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

- A new package, `internal/knowledge`, with one interface, `Store`, and plain data types: catalog entry, page, source, fact, search hit. Standard library only.
- Every piece of content carries its space and the space's label, normal or sensitive. A label that is missing reads as sensitive, so an adapter that forgets one cannot leak past rule 3.
- The calls: read the catalog, search, read a page, read a source, add a source with facts. Each takes a context and stops when it is cancelled.
- Capability flags, as a struct of yes or no values an adapter returns. The list comes from the plan and the later items: adding sources (P6-06), a catalog marker (P6-04), search with a query vector and taking page vectors (P6-08). A call whose flag is off answers "not supported".
- The `none` adapter. Reads come back empty, a page or source id is not found, and every optional call is not supported, so a caller treats it as any empty store.
- A registry where an adapter registers a constructor by name, and `Open`, which takes the chosen name as a plain argument. The name is the `knowledge.adapter` setting in a new lane file, `settings/knowledge.json`, with `none` as the default. Stored per-tenant values arrive with P2-04.
- Proof: a contract suite in `internal/knowledge/knowledgetest` that every adapter's tests call. It runs against `none`, and against an in-memory store that exists only in the suite's own tests. Copies of that store with one fault each must fail the suite, which shows the suite can fail.
- Docs: `docs/architecture/knowledge.md`, a row in the overview, decision records 0030 and 0031, and two lines in `verify`'s list of parts.


### Result

Done on November 24, 2026. Built as planned.

- `internal/knowledge`: the `Store` interface, the data types, the capability flags, the `none` adapter and the registry. `internal/knowledge/knowledgetest` is the contract suite.
- One setting, `knowledge.adapter`, default `none`, in `settings/knowledge.json`.
- The proof holds: `none` passes the suite, a correct in-memory store passes it with each flag on and off, and copies of that store with one fault each fail it.
- The lane's session stopped after its code and the part's doc were committed. The row in the overview, decisions 0030 and 0031 and the journal entry were written by the integrator from the plan and the doc.

Journal: `docs/journal/2026-11-24-knowledge-001.md`.
