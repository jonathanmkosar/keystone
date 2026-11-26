# 0414. Stale vectors are refreshed by a sweep in the turn runner, and the store lists the pages that need a vector of one model

November 25, 2026 · decided in P6-08

## The question

A page's vector goes stale when the page changes or when the embedding model changes. The interface's `VectorsNeeded` listed pages with no vector or a stale one, without naming a model: Waymarks counts a vector of another model as none, because it takes one model, but the built-in store keeps any model and would never list a page whose vector came from the old one (decision 0062 left this open). And the refresh needs to run somewhere: the jobs package, a sweep, or a hook on each write.

## The decision

**`VectorsNeeded(ctx, model, limit)`.** The interface names the model. The built-in store counts a vector of another model as none, as Waymarks does, so a model change lists every page again; an empty model keeps the old meaning. The Waymarks adapter does not send it: its instance knows its model, and refuses a vector of another when it is pushed. The contract suite passes the suite's model; the built-in store's own test proves a vector of another model is listed as none.

**A sweep in the turn runner.** `Runner.SweepEmbeddings`, started with the server as the incognito sweep is, refreshes every person once at the start and then every `knowledge.embedding_refresh_every`, in batches, up to a number of pages each time, one refresh for a person at a time. `POST /knowledge/embeddings/refresh` runs one now. The runner already opens models, records calls and asks the cap, so the refresh reuses that, and `internal/embedding` decides everything with no settings and no network.

**A row of state for each adapter**, `embedding_state`, replaced by each refresh: what was listed, embedded, held, stale, failed and left waiting, why it stopped, and the note the person is told. `GET /knowledge/embeddings` shows it.

## Why

- Naming the model in the call is what Waymarks does through its settings, and keeps the store the one place that knows which pages have which vectors.
- A sweep catches every way a page changes (the loader, an import, the upkeep jobs) without each writer knowing about vectors, and a model change without anyone telling the stores.
- The jobs package runs agent tasks in chats with turns; a refresh has no chat and no turn.

## What it costs

- A changed page waits for the next sweep, up to ten minutes by default, and is found by its words meanwhile, with its old vector still searched in the built-in store.
- The interface changed its signature: every adapter, and the suite, follow it.
