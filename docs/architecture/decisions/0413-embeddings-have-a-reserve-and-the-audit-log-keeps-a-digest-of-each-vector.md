# 0413. Embeddings have a reserve inside the monthly cap, and the audit log keeps the texts sent and a digest of each vector

November 25, 2026 · decided in P6-08

## The question

The background refresh is unattended spend, and needs its own small budget, counted under the cap like the memory pass's. The query of each message is an embedding call too. Fixed rule 4 says every model call is recorded: what was sent, what came back, tokens and cost. What came back is a vector of perhaps a thousand numbers for each page. What budget covers which calls, and what does the audit log keep?

## The decision

**One reserve for embeddings.** `knowledge.embedding_reserve_usd` ($0.50) is a part of the monthly cap, not an addition to it, as the memory pass's reserve is (decision 0280). Before a call to a model that is not local, `embedding.Allow` sums this month's embedding spend, `usage.SpentOn(month, "embedding")`, and refuses the call when that and the most it could cost pass the reserve; then the monthly cap is asked as for any call. It covers the refresh and the queries together: both are making vectors, a query costs a few tokens, and one number is easier to reason about than two.

**A refused call** is recorded as refused, costs nothing, and fails with the kind `spend_cap`: the refresh stops and leaves the rest waiting, and recall goes on by words. A local model is never asked about.

**Every call is recorded.** A started entry before it is sent, with the request in full (the model and every text), an end entry, and a usage record of the kind `embedding`. A call that cannot be recorded is not made.

**What came back** is kept as the model, the usage, and for each vector its size and the SHA-256 of its numbers as little-endian float32 values. The numbers are in the store. The digest ties a stored vector to the call that made it without copying every vector into a log that is never trimmed.

## Why

- The reserve is the memory pass's pattern, so the cap, the reserve and the records work the same way, and a person who sets either knows the other.
- A digest records what came back exactly enough to check it, at a hundredth of the size.

## What it costs

- The audit log does not hold the vectors, so a vector the store lost cannot be recovered from it; the next refresh makes it again.
- When the reserve is spent, recall by meaning stops for the rest of the month along with the refresh.
