# P6-07. Memory screens and incognito

Status: provisional
Lane: knowledge + interface
Depends on: P6-06
Owner gate: Approval of the memory mockups (P1-06), for the screens only.
Built without the gate: The server side and its tests; the screen built from the draft mockup with design tokens, marked provisional until the mockup is approved.
Scope: View, correct and forget; incognito chats with no record and no memory pass.
Proof: A forgotten fact is gone from the store and from recall; an incognito chat leaves nothing behind.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `knowledge/p6-07-memory-screens`, cut from `reboot` at 798e627.

### Plan

1. **The store can remove a source.** A new capability, `RemoveSource`, and a call of the same name on `knowledge.Store`: the source is gone from reads and search, and a page that cited it no longer does. The built-in store has it (secure delete on, the source index optimised). `none` and Waymarks do not: Waymarks keeps sources unchanged and only its owner can erase one, a token Keystone never holds. The contract suite gains a `remove source` check.
2. **Withdrawn sources.** For a store that cannot remove, Keystone keeps the ids it was asked to forget (`knowledge_withdrawn`, a new migration), and `knowledge.Open` hands back the store with those ids filtered out of search, reads and a page's citations, so recall, the lookups and the memory pass never see them again.
3. **View, correct and forget** (`internal/knowledge/memory/forget.go`, pure over the interface). What the memory pass added is listed from its own ledger (`memory_passes`), so only what Keystone's own intake added can be changed. Forgetting a fact removes its source (or withdraws it) and adds back the source's other facts as one new source in the same space; correcting does the same with the fact rewritten. The ledger row follows the new source. A profile line that says the forgotten fact goes, or is corrected. Rule 3: the new source must land with the old one's label, or it is taken back.
4. **Routes** (a new `internal/turn/memoryscreens.go`): `GET /memory` (the store, what it can do, the profile, the map, and each space's memories and pages), `POST /memory/forget`, `POST /memory/correct`.
5. **Incognito leaves nothing behind.** `POST /chats/incognito` makes a chat that is incognito from its first message; `POST /chats/{id}/close` deletes an incognito chat when the person leaves it (with the chats of tasks it started); a sweep deletes incognito chats idle past `knowledge.incognito_keep`. An incognito chat is left out of `search_chats` and `read_chat`, gets no recap, and its turns queue no memory pass (as before). Usage rows already hold no text; the audit log keeps its calls, as fixed rule 4 requires.
6. **Screens**, provisional on the memory mockups: a Memory screen at `/memory` (spaces, facts with correct and forget, pages, profile, map), an incognito switch on the new-chat screen, and a marker on an incognito chat that says it is deleted on leaving.
7. **Proof**: tests on the test tenant with the built-in store, and a `memory screens` check in `verify`: a forgotten fact is gone from the store and from recall; an incognito chat leaves no messages, no memory source, no recap, and is not found by past-chat search.

Left for later: forgetting a page (the interface has no page writes until P8-06); raising a Waymarks issue for a withdrawn source (it needs a token with the raise-issues grant, an owner question).

### Result

Provisional on the memory mockups (P1-06), for the screens only. The server side is complete and the proof passes on the test tenant with the built-in store: `verify` is 24 of 24 with the new `memory screens` check.

- Built as planned: `RemoveSource` with the contract suite's `remove source` check; withdrawn sources through `knowledge.Open`; `memory.Apply` for correct and forget over the memory pass's ledger, with the profile following; `GET /memory`, `POST /memory/forget`, `POST /memory/correct`; `POST /chats/incognito`, `POST /chats/{id}/close` and the sweep; no recap and no past-chat search for an incognito chat; the Memory screen, the incognito switch and mark.
- Decisions 0370 (a fact is changed by replacing its source; only the memory pass's own sources), 0371 (Waymarks: withdrawn in Keystone, kept facts back under the intake token, no issue yet), 0372 (incognito deleted when left or idle; its calls stay in the audit log).
- Seen in a browser on a server of this branch: forget, correct, and an incognito chat deleted on leaving.
- Not done: forgetting a page; a notice to Waymarks' agents for a withdrawn source; a real Waymarks.
- Doc: [memory-screens.md](../../architecture/memory-screens.md). Journal: `docs/journal/2026-11-25-knowledge-007.md`.
