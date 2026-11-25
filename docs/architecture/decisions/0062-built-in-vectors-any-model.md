# 0062. The built-in store takes a vector of any model and size, and compares them in Go

November 24, 2026 · decided in P6-02

## The question

The interface lets a store refuse an embedding model or a vector size it is not set up for. No embedding model exists in Keystone until P6-08. What does the built-in store take, and how does it search?

## The decision

**One vector for each page, of any model and any size.** The vector is stored with the name of the model that made it, its size, and the page revision it was made from. A new vector for a page replaces the old one.

**A query vector is compared only with stored vectors of the same model and the same size.** The rest are passed over, with no error.

**The comparison is cosine, computed in Go** over every stored vector that qualifies. There is no vector index and no SQLite extension.

**A stale vector is still searched.** A page that changed keeps its old vector, and stays findable by it, until the embedding job stores a new one.

**The store refuses** a vector with no model name, no numbers, a number that is not finite, or nothing but zeros.

## Why

- The store has no way yet to know which model is "the" embedding model. Taking any model, and never comparing across models, is safe whatever P6-08 chooses, and it needs no setting that would have to be invented now.
- One person's store is hundreds or thousands of pages. Reading that many small blobs and computing a cosine for each takes a few milliseconds. A vector index would be a new dependency or a C extension, which decision 0003 avoids.
- An old vector for a page is a better guide to the page than no vector.

## What it costs

- When the embedding model is changed, the pages still carry vectors of the old model. `VectorsNeeded` does not list them, because their revision is still the page's own, so searches by meaning find nothing until each page changes. P6-08 closes this: once the store can be told the model in use, a vector of another model counts as needing a new one. It is one more condition in one query.
- Search time grows with the number of pages. Past some tens of thousands of pages this wants an index.
- Only pages have vectors. A source is found by words alone.
