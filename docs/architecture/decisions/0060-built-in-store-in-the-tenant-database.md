# 0060. The built-in knowledge store keeps its pages in the tenant's database

November 24, 2026 · decided in P6-02

## The question

The plan describes the built-in store as "a markdown folder plus a SQLite index", in a `knowledge/` folder inside the tenant's folder. Where do the pages live in the first build: as files with an index beside them, or as rows?

## The decision

For now, as rows. A page's body is Markdown text in `knowledge_pages`, in the tenant's own `keystone.db`, with sources, labels, vectors and the FTS5 index in tables beside it. The adapter reaches them through the tenant's store handle and nothing else.

The folder is not built. This record does not change the plan: P6-02 is left provisional on this one point, with a question for the owner in the item file and in knowledge 002.

Names are kept ready for a folder. A space's name has no slash, and a page's path is words joined by `/` with no `.` or `..` part, so every page already has a place as `<space>/<path>.md`.

## Why

- The tenant's handle offers a database, `files/` and `artifacts/`. It has no `knowledge/` folder. Adding one is a change to `internal/tenant`, which the core lane owns and was working in during this item. Putting pages under `files/` would mix them with uploads that the chat record names by hash and removes with a chat.
- One place means one transaction. A page, its citations, its words in the index and the catalog marker change together or not at all. With files and an index, a crash between the two leaves them disagreeing, and the store needs a pass that walks the folder and repairs the index.
- The label is the guard of fixed rule 3. In the database it is read in the same statement as the content. With files, the content and its label come from two places.
- A backup of a tenant is still its folder, and isolation is still by construction (decision 0003).
- Everything the contract asks for is met without the folder.

## What it costs

- A person cannot open their pages in an editor, or keep them in their own git repo. That is the main thing a Markdown folder is for.
- The plan and the code disagree until the owner answers. If the answer is "files are the truth", the pages table becomes an index that is rebuilt from the folder, and the writing functions write files. The interface, the ids and the suite stay as they are.

## The choices for the owner

1. Rows are the truth, and Keystone can write the store out as a folder of Markdown files, and read one in. Recommended. It needs no change to the tenant handle beyond a place to write the export.
2. Files are the truth, and the database is an index over them. It needs a `knowledge/` folder on the tenant handle, a pass that brings the index up to date when a file changes outside Keystone, and a place in each file for the title, the kind and the citations.
