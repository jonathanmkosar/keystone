# 0091. The Waymarks catalog marker is a hash of the catalog and the labels

November 25, 2026 · decided in P6-03

## The question

`CatalogMarker` is a short text that stays the same while the catalog does. `knowledge.md` expected the Waymarks adapter to build it from the newest entry in Waymarks' change log, joined with the labels of its spaces. Does that hold?

## The decision

**The marker is a hash of the catalog as the reader sees it and of the label of every space the reader can read.** The adapter reads `GET /v1/catalog` and `GET /v1/spaces` and hashes each page's id, space, path, title and kind, then each space's name and label.

## Why

- The change log is trimmed to what the caller can read, and the trimming happens after the limit. Asking for the newest entry gives nothing when the newest change was in a space the reader cannot read. Two different states of the catalog can then give the same marker: nothing before a change the reader can see, and nothing again once a later change it cannot see has landed on top. A marker that can miss a change is worse than none.
- The hash cannot miss one. It is made from exactly the things the marker promises to follow.
- It does not move when only a page's body changes. The change log would, and the person map would be rebuilt for nothing.
- The catalog is one query in Waymarks, worked out when asked for.

## What it costs

- Two calls and the whole catalog on the wire each time, where the change log would be one small answer. The caller that asks for the marker is the one that would otherwise read the catalog and compare.
- If the catalog grows to tens of thousands of pages, Waymarks should offer a marker of its own, made the same way on its side.
