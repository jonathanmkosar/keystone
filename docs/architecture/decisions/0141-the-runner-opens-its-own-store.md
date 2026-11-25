# 0141. The turn runner opens a store of its own, for the tenant of the call

November 25, 2026 · decided in P4-03

## The question

The API shell gives a route's handler one tenant's store, opened for the call and closed when the call ends, and no install to open another from (decision [0040](0040-one-wrap-and-a-short-list-of-doors.md)). A turn goes on after the call that sent it has ended. It needs a store that lives as long as the turn does.

## The decision

**The runner is made with the install**, in `internal/app`, where the routes are listed. It is the one part behind the routes that holds it.

**It opens a store by one id only: `TenantID()` of the store the call came with.** That id came from the session, in the shell. The runner reads no tenant id from a path, a query, a header or a body. After a restart it opens each tenant in the registry in turn, to finish that tenant's own turns.

**A goroutine has one store, for one chat's queue, and closes it when the queue is empty.** It never holds two tenants' stores, and what it reads from one it writes to the same one.

**A handler still reads and writes through the store the shell gave it.** Only the work that outlives the call uses the runner's.

## Why

- The alternative is to keep the call's store open after the call. The shell closes it, and should: a handler that could hold a store open has a way to leak one.
- A second handle on the same database file is what SQLite's write-ahead mode is for. Readers do not wait for the writer, and two writers wait for each other for up to the busy timeout.
- The route walk in `tenant isolation` still proves what it proved: a call made with tenant one's session, naming tenant two wherever a request can, writes to tenant one's log and not to tenant two's. `calls recorded` now also proves a turn's model call lands in the sender's log and nowhere else.

## What it costs

- The promise "a handler has no way to another tenant" now rests on the runner's code as well as on the shell's. The runner's only way to a tenant is the id of a store it was handed, and the checks above watch it.
- Each chat with a turn running holds one more open database handle for as long as the turn runs.
