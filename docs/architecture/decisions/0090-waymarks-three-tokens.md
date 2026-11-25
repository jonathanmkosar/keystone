# 0090. The Waymarks adapter holds three tokens, and its capabilities follow them

November 25, 2026 · decided in P6-03

## The question

The plan gives Waymarks one token for each Keystone role and names two: a reader for chat and sub-agents, and an intake token for the memory pass. Waymarks counts storing a page's vector as writing a page, which neither grant allows. Which token stores vectors, and how does the adapter know what it can do?

## The decision

**Three tokens, each a secret in the tenant's vault.** `knowledge.waymarks.reader`, `knowledge.waymarks.intake` and `knowledge.waymarks.embedder`. The reader is needed. The other two are optional.

**Each call goes out under the token of its role.** Reads and searches under the reader, a new source under the intake token, and the list of pages needing a vector and each stored vector under the embedder.

**The capabilities are set from which tokens are there, when the store is opened.**

| Token | Switches on |
| --- | --- |
| reader | The store, and `CatalogMarker` |
| intake | `AddSource` |
| embedder | `PageVectors` and `VectorSearch` |

**A search by vector is sent under the reader and offered only when the embedder is there.**

**With no reader token the store does not open**, and the error is `ErrUnavailable`, naming the secret.

## Why

- Giving the reader the right to write pages, so it could store vectors, would hand chat and every sub-agent a token that can change the wiki. The reader stays a reader.
- The intake grant reads nothing, by Waymarks' design, so the memory pass cannot be turned into a way to read. It cannot list pages or read one to embed it, so it cannot be the embedder either.
- The adapter cannot ask Waymarks whether the instance takes vectors: that setting is for its admin. The embedder token is the sign a person set vectors up. Without it nobody pushes a vector, a search by vector would find nothing, and Keystone would make a query vector for no reason.
- An adapter opened with fewer tokens is the same code with flags off, which the contract suite already checks.

## What it costs

- A third token to issue in Waymarks and to store, for a person who wants search by meaning.
- The flags follow the tokens and not the grants behind them. A token with a narrower grant than its role needs gives `ErrUnavailable` on the call, not a flag that is off.
- The plan names two roles. The embedder is a third, added here. The plan's sentence on Waymarks tokens should name it once the owner agrees.
