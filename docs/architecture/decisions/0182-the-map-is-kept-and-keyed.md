# 0182. The map is kept in the tenant's database under a key, and is not used when the store is down

November 25, 2026 · decided in P6-04

## The question

The map is wanted at the start of every call, and the catalog it is made from may be thousands of pages behind a network call. When is it built, where is it kept, and what happens when the store cannot be read?

## The decision

**The map is built when it is asked for and found out of date, and kept in one row of `person_map` in the tenant's own database.** No job builds it ahead of time.

**The row carries a key for the state of the catalog it was built from.** For a store with the `CatalogMarker` capability the key is the marker. For a store without it the key is a hash of the catalog as read: each entry's id, space, label, path, title and kind. The row also carries the limits and the layout it was built with. A row whose key or limits are not today's is replaced.

**The marker is read before the catalog.** If the catalog changes between the two reads, the map is newer than its key and is built once more next time. The other order could keep an old map under a new key.

**When the store cannot be read, the map is empty for every model.** The kept row is left alone and not used. The reason is returned beside the empty map, and no error.

## Why

- Asking the marker is one cheap call for the built-in store. Building on demand means there is no job to schedule, and no window where a job has not yet run.
- A hash of the catalog serves a store with no marker with the same code path, and it also covers a person who changes adapter: the key will not match.
- Labels are read fresh everywhere else (decision 0031). A kept open version is safe to use only because the marker, read just now, says no label has changed. With the store down nothing says so. A space the person marked sensitive an hour ago would still be in the kept open version.
- The kept whole version would be safe to use with the store down, since its reader may read everything. It is still not used, so that one rule covers both and the model is not told about pages it cannot then fetch.
- A chat must carry on without the store, so a store that is down is not an error.

## What it costs

- The first call after a change waits for a catalog read. For Waymarks that is the marker's two calls and then the catalog again.
- A store with no marker has its whole catalog read on every call. Only an adapter not yet written would be in that position: `none` has no pages, and both real adapters have the marker.
- The built-in store's marker does not move if a row of `knowledge_spaces` is removed by hand, outside the store's own functions. The kept open version would then be stale until the next real change. Nothing in Keystone removes that row.
- With the store down the model gets no map at all, where a stale one might have helped.
