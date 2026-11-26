# Embeddings

Folders: `internal/embedding`, joined to Keystone by `internal/turn/embeddings.go`; the embed call in `internal/seam/embed.go`, `internal/adapters/embed.go`, `internal/adapters/fake/embed.go` and `internal/adapters/chatcompletions/embed.go`; recall by meaning in `internal/knowledge/recall/meaning.go`

Waymarks runs no embedding pipeline, and neither does the built-in store: Keystone makes the vectors. An embedding model is a registry entry with the `embedding` capability. In the background, Keystone gives every page of the person's knowledge store a vector from that model and pushes it to the store, which keeps it and searches with it. With each message, recall turns the message into a vector with the same model and searches by meaning as well as by words. A page in a sensitive space is embedded only by a local or trusted model.

With no embedding model, nothing here happens, and recall is exactly as it was.

Decisions: [0410](decisions/0410-embedding-is-a-call-of-its-own-on-the-seam.md) for the embed call and the adapters, [0411](decisions/0411-words-and-meaning-are-merged-by-weighted-reciprocal-rank.md) for how the two searches are merged, [0412](decisions/0412-the-embedding-model-is-chosen-local-first-and-sensitive-pages-wait-for-one.md) for which model and which pages, [0413](decisions/0413-embeddings-have-a-reserve-and-the-audit-log-keeps-a-digest-of-each-vector.md) for spend and what the audit log keeps, and [0414](decisions/0414-the-refresh-is-a-sweep-and-the-store-lists-by-model.md) for the refresh.

## The embed call

`internal/seam/embed.go`. An embedding model writes no reply, so the call has its own shape:

| Type | Holds |
| --- | --- |
| `EmbedRequest` | `model`, the registry name, and `texts`, none blank. |
| `EmbedResponse` | `model`, `vectors`, one for each text in order and all of one size, and `usage`, of which only `input_tokens` is ever more than zero. |
| `Embedder` | `Embed(ctx, EmbedRequest) (EmbedResponse, error)`. A failed call returns a `*seam.Error`, as a `Provider` does. |

`ValidateEmbedResponse` refuses a missing or extra vector, two sizes, an empty vector, a number that is not finite, and a vector of nothing but zeros.

An adapter whose provider can embed has the `Embed` method beside `Stream`. `adapters.OpenEmbedder(ctx, store, set, name)` opens a registry entry as one, and refuses an entry without the `embedding` capability or an adapter that makes no vectors (`ErrNotEmbedder`). `adapters.Embeds(adapter)` says which do: `fake` and `chat_completions`. The routing rule already in place holds: an embedding model takes embedding calls and nothing else, and a chat model takes none ([routing.md](routing.md)).

### The Chat Completions adapter

`POST {endpoint}/embeddings` with `{"model": <provider_model>, "input": [...], "encoding_format": "float"}`, the OpenAI-style call that LiteLLM, Ollama's compatible server, OpenRouter and the home gateway answer. Each vector goes to the text its `index` names, or in order when none has an index. `usage.prompt_tokens` (or `total_tokens`) is the input; with neither, Keystone counts as the budget does and marks the count estimated. A refusal becomes the seam error of its kind, a retryable one is sent again as a chat call is, no redirect is followed, and the key is never in a message. Proven against hand-written exchanges in `fixtures/chat-completions/embeddings/` replayed by a server on this machine ([adapter-chat-completions.md](adapter-chat-completions.md)).

### The fake

The fake's vectors are deterministic. Each word of a text, with a plural or verb ending taken off, is hashed to one place of the vector with a sign; a word of a meaning group stands for its group and weighs four times another word; the vector is made unit length. So two texts are near when they share meanings, and a question can find a page that holds none of its words. The groups are test data, `fake.Options.Meanings`; the size is `fake.embedding_size` ([fake-provider.md](fake-provider.md)).

## Which model, and which pages

`internal/embedding` decides, and reads no setting. It is a guard of `rules.sensitive_content` in `tools/verify/fixedrules.go`: `verify` fails if it ever imports `settings` or `internal/tenantsettings`, itself or through another package.

| Function | Does |
| --- | --- |
| `Choose(named, usable, can)` | The model: `routing.embedding_model` when it names an embedding model the person can use, whatever its class; otherwise the first local embedding model by name, then the first trusted one. A hosted one is never chosen unasked. With none, `ErrNoModel`. |
| `MayEmbed(label, model)` | Fixed rule 3 for one page: a normal page goes to any embedding model, a sensitive one only to a model that may read sensitive content. A blank or unknown label reads as sensitive. |
| `MayEmbedQuery(held, model)` | The same for recall's query: the message of a chat that holds sensitive content goes only to a model that may read it. |
| `VectorModel(model)` | The name a vector carries in the store: the entry's `provider_model`, or its registry name when it has none. |
| `PageText(page, max)` | What a page's vector is made from: its title, then its body, cut at a word's end. |
| `Refresh(ctx, store, model, embed, options)` | The one way pages reach an embedding model, below. |
| `NewReserve`, `Allow` | The embedding reserve, below. |
| `Save`, `Load`, `Note` | Where a person's refresh stands, and what they are told. |

One model makes all of a person's vectors, so a query vector can be compared with every page's. When that model is hosted, the pages in a sensitive space are left without a vector: recall still finds them by their words, and the person is told how many, and how to have them searched by meaning too.

## The refresh

`Refresh` makes the vectors a person's store is missing, or holds stale, with the chosen model:

1. The store lists the pages with no vector of the model's, or a stale one: `VectorsNeeded(ctx, VectorModel(m), knowledge.embedding_list_max)`. A vector of another model counts as none, so when the model changes every page is listed again ([knowledge.md](knowledge.md)).
2. A page the model may not read, by the label it was listed with, is held and never read.
3. The rest are read a batch at a time (`knowledge.embedding_batch_size`), at most `knowledge.embedding_pages_per_refresh` of them, and a page whose label now says sensitive is held too: the label is asked twice.
4. A batch's texts go to the model, and each vector to the store for the revision that was read. A page that changed meanwhile is stale and waits for the next refresh.

A failed call, a refusal by the reserve or the cap, and a store that refuses the vectors (a Waymarks instance whose owner chose another model) stop the refresh; what was stored stays, and the rest waits.

### When it runs

`internal/turn/embeddings.go`:

- **In the background.** `Runner.SweepEmbeddings`, started with the server in `internal/app`, refreshes every person once at the start and then every `knowledge.embedding_refresh_every` (10 minutes). Its calls are recorded under a request id of their own, `embeddings-<hex>`, with the actor `embeddings`.
- **Now.** `POST /knowledge/embeddings/refresh` runs the person's refresh and answers with where it stands.

One refresh runs for a person at a time. A person with no store, `knowledge.embeddings` off, no embedding model, or a store without the `PageVectors` capability sends nothing, and the answer says why.

The state of the last refresh is kept in `embedding_state`, one row for each adapter, from the migration `20261125T234753_embeddings.sql`: the model, its class, what was listed, embedded, held, stale, failed and left waiting, why it stopped, and the note the person is told. The note names no page, since a title may be sensitive.

### The stores

| Store | Vectors | Search by vector |
| --- | --- | --- |
| Built-in | In its own table, `knowledge_page_vectors`, one for each page, with the model and the revision. Any model and size. | In Go, cosine against each vector of the query's model and size; the score is the cosine. |
| Waymarks | Pushed under the embedder token, `PUT /v1/pages/{id}/vector`, after `GET /v1/vectors/needed` ([knowledge-waymarks.md](knowledge-waymarks.md)). Its owner sets the one model and size it takes. | `POST /v1/search/hybrid` with the vector, under the reader token; the score is its place, by reciprocal rank. |

## Recall by meaning

`internal/turn/embeddings.go` (`meaningFor`) and `internal/knowledge/recall/meaning.go`. When the person's store takes a query vector (`VectorSearch`), `knowledge.recall_meaning` is on and there is an embedding model, recall makes a vector of the same query it searches words with (the message's words, without the ignored ones) and gives it to `recall.Find` as `Options.Meaning`:

1. The store is searched by words, as before, and by the vector alone, for at most `knowledge.recall_meaning_max_results` pages.
2. Each list keeps its own threshold on its own scale: `knowledge.recall_min_score_<adapter>` for words, `knowledge.recall_meaning_min_score_<adapter>` for meaning (a cosine of 0.3 for the built-in store; 0 for Waymarks, which gives places and not nearness).
3. The lists are merged by weighted reciprocal rank: a hit scores `weight / (k + its place among the hits of that list that pass)`, summed over the lists, with `knowledge.recall_merge_words_weight`, `knowledge.recall_merge_meaning_weight` and `k` = `knowledge.recall_merge_rank_constant` (60, as Waymarks). A page both find is one hit, labelled sensitive when either said so.
4. Recall reads the merged list as it read the words' list: each hit labelled, filtered for the models of the call by `Found.For`, so fixed rule 3 is untouched.

The record for the turn inspector says the model, how many pages the meaning found and its threshold, and for each hit `found_by` (`words`, `meaning` or `both`), its meaning score and its merged score. When there is a model and no vector was made, `meaning_note` says why: the chat holds sensitive content and the model may not read it, the call failed or was refused, or the search by meaning failed. With no model, nothing of meaning is recorded.

The query's call is made inside `knowledge.recall_timeout`, under the turn's request id, asked of the reserve and the cap like the refresh's. A chat whose route is sensitive (it was sent sensitive content, or holds a sensitive attached chat) sends its query only to a local or trusted embedding model.

## Spend, and what is recorded

Every embedding call is recorded as fixed rule 4 asks: a started entry in the audit log before it is sent, with the texts in full; an end entry with the model, the usage, and for each vector its size and the SHA-256 of its numbers (the numbers themselves are in the store); and a usage record of the kind `embedding`, with its cost from the entry's input price. A call that cannot be recorded is not made.

Before a call to a model that is not local, the embedding reserve is asked, then the monthly cap. `knowledge.embedding_reserve_usd` ($0.50) is a part of the cap, not an addition to it, as the memory pass's is: what embeddings spend on models that are not local, the refresh and the queries together, counts against both. A refused call is recorded as refused and costs nothing; the refresh stops and the rest waits, and recall goes on by words. A local model is never asked about.

## Routes

In the shell like every route ([api.md](api.md)), listed in `internal/app`:

| Route | Reply |
| --- | --- |
| `GET /knowledge/embeddings` | `EmbeddingsReply`: the adapter, `on`, the model (name, label, class, vector model, whether it may read sensitive content), whether the store takes vectors and whether recall searches by meaning, the last refresh, and the note. |
| `POST /knowledge/embeddings/refresh` | The same, after a refresh now, with `refreshed`. No body. |

## Settings

In `settings/knowledge.json`, with `routing.embedding_model` in `settings/models.json` and `fake.embedding_size` there too.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `routing.embedding_model` | empty | tenant | The embedding model. Empty: the first local one, then the first trusted one. |
| `knowledge.embeddings` | true | tenant | Whether the refresh makes vectors. |
| `knowledge.embedding_batch_size` | 16 | install | Pages in one call, 1 to 256. |
| `knowledge.embedding_pages_per_refresh` | 200 | install | Pages one refresh sends, 1 to 100000. |
| `knowledge.embedding_list_max` | 1000 | install | Pages the store is asked to list, 1 to 100000. |
| `knowledge.embedding_text_max_chars` | 8000 | install | Characters of one page sent, 100 to 1000000, less when the model's window holds less. |
| `knowledge.embedding_reserve_usd` | 0.5 | tenant | The embedding reserve, a part of the monthly cap. |
| `knowledge.embedding_refresh_every` | 10m | install | How often the refresh runs, 1m to 24h. |
| `knowledge.embedding_timeout` | 2m | install | How long one call of the refresh may take, 5s to 30m. |
| `knowledge.recall_meaning` | true | tenant | Whether recall searches by meaning too. |
| `knowledge.recall_meaning_max_results` | 5 | tenant | Pages the search by meaning finds, 1 to 100. |
| `knowledge.recall_meaning_min_score_built_in` | 0.3 | tenant | The cosine a page must reach. |
| `knowledge.recall_meaning_min_score_waymarks` | 0 | tenant | Waymarks' threshold, on its scale of places. |
| `knowledge.recall_merge_words_weight` | 1 | tenant | The words' weight, 0 to 100. |
| `knowledge.recall_merge_meaning_weight` | 1 | tenant | The meaning's weight, 0 to 100; not both 0. |
| `knowledge.recall_merge_rank_constant` | 60 | install | k, 1 to 1000. |
| `fake.embedding_size` | 1024 | install | The size of the fake's vectors. |

## Checked by

`go test ./internal/seam ./internal/adapters/... ./internal/embedding ./internal/knowledge/... ./internal/turn`:

- The embed request and answer are checked and go through JSON. The fake's vectors are the same each time, unit length, near for texts that share a meaning and far for unrelated ones. The Chat Completions adapter sends each hand-written exchange's body to `/embeddings`, reads each answer, turns each refusal into its kind and retries the retryable ones, refuses each malformed answer, and sends nothing for a chat model, a blank text or a cancelled call, nor on a redirect.
- `Choose` takes the model named, then local, then trusted, never a hosted one unasked, never a chat model. `MayEmbed` and `MayEmbedQuery` keep sensitive content from a hosted model. `Refresh` sends a hosted model the normal pages in batches and never the sensitive one, asks the label again when the page is read, stops at a refusal and leaves the rest waiting, and embeds every page again for another model. The reserve counts only embedding calls to models that are not local.
- The built-in store lists a page whose vector came from another model as having none of this one.
- Recall finds a page by meaning that shares no word with the question, merges a page both searches find to the top, holds each list to its threshold, withholds a sensitive page found by meaning, and goes on by words when the search by meaning fails.
- In a turn: a local embedding model's refresh embeds every page; a question by meaning recalls its page, with the call's audit entries and usage record; with recall by meaning off it does not. A hosted embedding model is not chosen unasked, is never sent the sensitive page or a held chat's query, the person is told, and the reserve stops a refresh and records the refused call. With no embedding model nothing changes and nothing is recorded.

In `verify`: the standing check `embeddings` ([tools.md](tools.md)), and `internal/embedding` as a guard in the `fixed rules` check.

## Not proven yet

- **A real embedding model.** Only the fake and a stand-in server have made vectors. The live check is a local embedding model on the home gateway, and the owner's embedder token for his Waymarks.
- **Thresholds on real scores.** The built-in store's 0.3 is a guess for real cosines; Waymarks gives places, so its nearest pages join every message, up to `knowledge.recall_meaning_max_results`. An owner question asks for a nearness from Waymarks.
- **A model's fixed size.** The registry entry has no vector size. A Waymarks instance takes one size, set by its owner, and refuses another.
- **Many held pages.** A refresh lists at most `knowledge.embedding_list_max` pages. A store with more sensitive pages than that before the first normal one that needs a vector would wait behind them with a hosted model.

## Shared code changed

Every existing line of shared code this item changed:

- `internal/knowledge/knowledge.go`: `VectorsNeeded` takes the model (`VectorsNeeded(ctx, model, limit)`), and its comment. `none.go`, `builtin/search.go`, `waymarks/calls.go`, `knowledgetest/suite.go` and `knowledgetest/memory_test.go` follow.
- `internal/knowledge/recall/recall.go`: `Options.Meaning`; `CheckOptions` asks `checkMeaning` first; three fields on `HitRecord`, four on `Record`; three on `candidate` and one on `Found`; in `Find`, the hits are ranked by `f.rank` before the loop, which reads `r.passes`; in `For`, one line each for the record and the hit.
- `internal/turn/recall.go`: one line in `searchKnowledge`, `o.Meaning = r.meaningFor(...)`, before the search.
- `internal/adapters/fake/fake.go`: two fields on `Options`, `DefaultOptions` reads `fake.embedding_size`, one field on `Provider`, and `New` builds the meaning groups.
- `internal/app/app.go`: the routes and the sweep, five added lines.
- `tools/verify/main.go` (the check and two `componentDocs` lines), `tools/verify/fixedrules.go` (an `init`), `tools/verify/waymarksfake.go` (a field and three routes).
- `settings/knowledge.json` (fifteen entries) and `settings/models.json` (one).
