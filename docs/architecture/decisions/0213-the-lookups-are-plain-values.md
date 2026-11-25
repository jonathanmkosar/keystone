# 0213. The lookups are plain values the tool loop wraps, and take the model's answer like recall

November 25, 2026 · decided in P6-05

## The question

The plan gives the agent lookup tools to go deeper than recall. The tool loop and its registry (P7-01) were being built at the same time, in another worktree. What shape do the lookups take, how do they keep fixed rule 3, and do they get a view of the store that cannot add?

## The decision

**Each lookup is a plain `Lookup` value**: a name, a description written for the model, a JSON Schema, `ReadOnly` true, and a run function that takes a context, the tenant's store handle, the opened knowledge store and the model's input as raw JSON, and returns a `Result`. There are three: `knowledge_search`, `knowledge_read_page` and `knowledge_read_source`. Nothing here is a registry, and nothing registers them.

**`Lookups(mayReadSensitive, options)` makes them for one model**, with the same plain answer `Found.For` takes, and is the other way to the store's content. For a model that may not read sensitive content, a search leaves sensitive hits out and says how many, and a read of a sensitive page or source says only that this model may not read it, naming neither the title nor the space.

**A result that holds sensitive content says so**, and the caller marks the chat with `recall.Hold` (decision 0211).

**No view of the store that cannot add is made.** The lookups and recall call only `Search`, `ReadPage` and `ReadSource`.

## Why

- Plain data and one function need only a thin wrapper in any registry, whatever shape P7-01 gives it, and they can be tested here without one. Building a registry here would have raced the one being built.
- The run function takes the store as an argument, so the tool loop opens it once for the turn and closes it, and the lookups hold no state.
- Taking the answer when the list is made, not when a lookup runs, means a model's list cannot be run with another model's answer. The answer to pass is the least of the call's route, as for recall.
- A refusal that names nothing is the same rule as the open map's (decision 0181): a title is content.
- An input field the lookup does not know is refused, not ignored, so a model's mistake is told back to it rather than guessed at.
- A view that cannot add would guard a door no code here opens. Waymarks already sends reads and adds under different tokens (decision 0090), and the built-in store adds only through calls the memory pass makes.

## What it costs

- Until P7-01 registers them, no model can call them. The integrator's step is in the journal.
- A model is told that sensitive results exist, and how many. As for the map, a number names nothing, and it tells the person a local model could see more.
- A lookup reads a long page in parts the model must ask for, one call each.
