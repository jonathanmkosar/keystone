# 0630. A decision is a call of its own on the seam, and a decision model lists `decision` alone and takes nothing else

November 27, 2026 · decided in P14-02

## The question

The spec adds a capability, `decision`, and a seam interface, `Decide`. A decision model takes a state and typed questions and answers with probabilities; it writes no reply. How does it sit beside `Provider` and the registry, and what may an entry with `decision` also claim?

## The decision

**A call of its own, as an embedding is** (decision [0410](0410-embedding-is-a-call-of-its-own-on-the-seam.md)). `seam.DecisionRequest`, `seam.DecisionResult` and a `Decider` interface with one method, `Decide`, in a new file, `internal/seam/decide.go`. An adapter's provider decides when it has the method; `adapters.OpenDecider` opens an entry as `Open` does and asks for it. The `Set` of builders and `seam.Request` are unchanged, so the effort work on the seam (P14-01) touches none of the same lines.

**A decision model lists `decision` and nothing else.** `models.Check` refuses an entry with `decision` and any other capability, and refuses a `systemone` entry without `decision`. Routing's `fits` withholds a decision model from every kind but `decision`, and a model without the capability from `decision`, as it does for embedding. The turn's candidates leave decision models out, so the server never offers one for a chat.

## Why

- A message-shaped request would carry roles, tools and a stream that mean nothing to a decision model.
- Ollama's decision API takes no tools and writes no text, so `tools`, `vision` or `thinking` on a decision entry would be a claim no call could use. Refusing the mix when the entry is added says so at once, where an embedding entry's mix is only found when it is opened.
- Keeping the seam change in new files keeps two lanes working on the seam at once from colliding.

## What it costs

- An entry that wants a model to decide and chat needs two entries, one of each. Ollama serves the two from one address, so that is two short entries with one endpoint.
- Embedding entries are not held to the same rule; a later item may.
