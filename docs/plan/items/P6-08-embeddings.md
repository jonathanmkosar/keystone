# P6-08. Embeddings

Status: provisional
Lane: knowledge + models
Depends on: P3-02, P6-03, P6-05
Owner gate: none in the plan; for the live check only, a real local embedding model on the home gateway and the owner's Waymarks embedder token (knowledge 008).
Scope: Embedding as a model seam capability with its own registry entries, local first. Page vectors are pushed to stores that take them (Waymarks runs no embedding pipeline of its own). Recall gets a query vector, so search is by meaning as well as by keyword. Stale vectors are refreshed in the background. A sensitive page is embedded only by a local or trusted model.
Proof: Recall by keyword and meaning answers more of the test tenant's recall questions than keyword alone, and no sensitive page reaches a hosted embedding model.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

Branch `knowledge/p6-08-embeddings`, cut from `reboot` at 8106f15. Built on the Waymarks adapter (P6-03), whose only open part is the live check with the owner's tokens; nothing here rests on his Waymarks or his tokens.

### Plan

1. **Embedding on the seam.** A small request shape of its own in `internal/seam` (`EmbedRequest`: model and texts; `EmbedResponse`: one vector for each text, and usage) and an `Embedder` interface beside `Provider`. A registry entry declares it with the `embedding` capability, which it already has; such an entry takes embedding calls and nothing else (routing's rule). `adapters.OpenEmbedder` opens one.
2. **Adapters.** The fake makes deterministic vectors: words hashed into a fixed size (`fake.embedding_size`), with groups of words it takes to mean the same thing given as test data, so a question can find a page by meaning with no word in common. The Chat Completions adapter speaks the OpenAI-style `POST {endpoint}/embeddings`, which local gateways, LiteLLM and Ollama's compatible server answer, proven against hand-written exchanges and a stand-in server on this machine.
3. **The guard** (`internal/embedding`, a new package that imports no settings, listed in `tools/verify/fixedrules.go`): which model embeds (`routing.embedding_model`, or with none named the first local embedding model, then the first trusted; a hosted one only when named), whether a page may go to it (a sensitive page only to a local or trusted model), whether a held chat's query may, and the refresh itself over the knowledge interface. With no local or trusted embedding model, sensitive pages are left unembedded, counted, and the person is told.
4. **Vectors to stores that take them.** `VectorsNeeded` is given the embedding model, so the built-in store lists a page whose vector came from another model as needing one, as Waymarks already does; the built-in store keeps the vectors in its own table and searches them in Go; the Waymarks adapter pushes them under the embedder token. A new migration keeps the state of each person's refresh (`embedding_state`).
5. **Recall by meaning.** Recall embeds its query with the same model as the pages, searches by words and by the vector apart, and merges the two lists by weighted reciprocal rank (a decision record), each list with its own threshold. With no embedding model, or a store without `VectorSearch`, recall is exactly as before. A held chat's query goes only to a local or trusted embedding model.
6. **Refreshed in the background.** A sweep the server starts (`knowledge.embedding_refresh_every`) refreshes stale and missing vectors for every person, in batches, under its own spend reserve (`knowledge.embedding_reserve_usd`, a part of the monthly cap, like the memory pass's), and then the cap. Every embedding call has its audit entries and a usage record of the kind `embedding`. `GET /knowledge/embeddings` says where it stands and `POST /knowledge/embeddings/refresh` runs it now.
7. **Proof.** Tests in each package, and a standing check `embeddings` in `verify`: on the test tenant with the fake embedding model, keyword and meaning answer more of the fixture's recall questions than keyword alone (new questions keyword search misses, invented), and a hosted embedding model behind a stand-in server is never sent a sensitive page, nor a held chat's query.

Left for later: a real local embedding model and the owner's embedder token for the live check; an embedding model's own fixed vector size in the registry (Waymarks is set to one by its owner).

### Result

Provisional on the live check only: a real local embedding model on the home gateway, and the owner's Waymarks embedder token with the instance set to the same model and size. The proof passes on the test tenant with the fake embedding model, on both stores: `verify` is 27 of 27 with the new `embeddings` check, where words alone answer 3 of the 6 questions and words and meaning answer all 6, and a hosted embedding model behind a stand-in server is never sent a sensitive page or a held chat's message.

- Built as planned: `seam.EmbedRequest`, `EmbedResponse` and `Embedder`; `adapters.OpenEmbedder`; the fake's vectors with meaning groups (`fake.embedding_size`); the Chat Completions adapter's `POST {endpoint}/embeddings`, proven against hand-written exchanges; the guard `internal/embedding` (`Choose`, `MayEmbed`, `MayEmbedQuery`, `Refresh`, the reserve, the state), listed in `fixed rules`; `VectorsNeeded(ctx, model, limit)`; recall by words and meaning merged by weighted reciprocal rank; every embedding call recorded with the kind `embedding` under its own reserve inside the cap; the background sweep and `GET /knowledge/embeddings`, `POST /knowledge/embeddings/refresh`; the migration `20261125T234753_embeddings.sql`; fifteen settings in `settings/knowledge.json` and one in `settings/models.json`.
- Decisions 0410 (the embed call and the adapters), 0411 (the merge), 0412 (the model, local first, and the sensitive rule), 0413 (the reserve and what the audit log keeps), 0414 (the refresh and `VectorsNeeded` by model).
- Not done: a real embedding model; a real Waymarks; a screen for the status (the route is ready); a nearness from Waymarks for hits found by vector, an owner question.
- Doc: [embeddings.md](../../architecture/embeddings.md). Journal: `docs/journal/2026-11-25-knowledge-008.md`.

