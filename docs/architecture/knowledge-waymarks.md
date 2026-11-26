# The Waymarks adapter

Folder: `internal/knowledge/waymarks`

This is the knowledge store adapter for Waymarks, the owner's own wiki product. It is an HTTP client of one Waymarks instance, and it is the adapter named `waymarks`. A tenant chooses it by setting `knowledge.adapter` to `waymarks`. Each tenant has an instance of their own.

It implements `knowledge.Store`, which is described in [knowledge.md](knowledge.md), and it passes the contract suite against a fake Waymarks. It has not been run against a real one: see "Not proven yet".

Decisions: [0090](decisions/0090-waymarks-three-tokens.md) for the three tokens and what each switches on, [0091](decisions/0091-waymarks-marker-is-a-hash-of-the-catalog.md) for the marker, [0092](decisions/0092-waymarks-labels-from-the-answer.md) for where a label comes from, and [0093](decisions/0093-waymarks-refusals.md) for what each refusal becomes.

## Where the API came from

The adapter is written against Waymarks' own source, read on the owner's machine: its API doc, its route list, its handlers and its Go client. Nothing was copied from it but the shape of the calls. No call is guessed. What has not happened is a call to a running Waymarks, so the reading is unproven where the source could be misread.

## What a tenant sets

| Setting | Default | What it is |
| --- | --- | --- |
| `knowledge.waymarks_address` | empty | Where the instance is, such as `http://127.0.0.1:7878`. `http` or `https`, a host, and maybe a path the instance is served under. A name, a password, a `?` or a `#` in it is refused. |
| `knowledge.waymarks_timeout` | `5s` | How long one request may take, from `100ms` to `2m`. |
| `knowledge.waymarks_search_max_results` | 20 | The limit of a search that names none, from 1 to 100. Waymarks allows 100 at most. |
| `knowledge.waymarks_vectors_needed_max` | 100 | The limit of a list of pages needing a vector that names none, from 1 to 1000. For the whole install. |
| `knowledge.waymarks_reply_max_mb` | 32 | The largest answer the adapter reads. For the whole install. |

The settings are read once, when the store is opened.

## The tokens

The tokens are secrets in the tenant's vault, read by name through `Store.Secrets()` when the store is opened. They are never in a setting, never in the environment, and never in the address.

| Secret | Role | The grant it needs in Waymarks | What it switches on |
| --- | --- | --- | --- |
| `knowledge.waymarks.reader` | Chat and sub-agents | Read and search | The store itself, and `CatalogMarker`. Without it the store does not open. |
| `knowledge.waymarks.intake` | The memory pass | Add sources. It reads nothing. | `AddSource` |
| `knowledge.waymarks.embedder` | The embedding job | Read and write pages | `PageVectors` and `VectorSearch` |

Store one with the tenant command, which reads the value from standard input:

    go run ./tools/tenant secret set <tenant-id> knowledge.waymarks.reader

Each call goes out under the token of its role and no other. A test reads the fake's record of who called what.

A search by vector goes out under the reader. It is offered only when the embedder token is there, because an instance nobody pushes vectors to has none to search.

A sensitive space is reached only by a token whose grant names it. That is Waymarks' rule. A sensitive space the reader's grant does not name is not in the store as Keystone sees it: its pages are not listed, not found by a search, and a read of one by id is `ErrNotFound`.

## The calls

All under `/v1`, with `Authorization: Bearer <token>`. Every answer is `{"data": ...}`, with `"sensitive": true` and the header `Waymarks-Sensitive: true` when anything in it came from a sensitive space.

| Here | The call | Token |
| --- | --- | --- |
| `Catalog` | `GET /catalog`. The kind is the page property `kind`. | reader |
| `Search` with words | `GET /search?q=&space=&limit=` | reader |
| `Search` with a vector | `POST /search/hybrid` with `q`, `vector`, `model`, `space` and `limit` | reader |
| `ReadPage` | `GET /pages/{id}`. The revision is `rev`, and the citations are `sources`. A deleted page is not found. | reader |
| `ReadSource` | `GET /sources/{id}/text`, which answers with the record and the text together. An erased source is not found. | reader |
| `AddSource` | `POST /sources` as `multipart/form-data`: the fields, then the file. | intake |
| `CatalogMarker` | `GET /catalog` and `GET /spaces`, hashed. | reader |
| `VectorsNeeded` | `GET /vectors/needed?limit=` | embedder |
| `PutVector` | `PUT /pages/{id}/vector` with `rev`, `model` and `vector` | embedder |
| labels | `GET /spaces`, only when an answer over several spaces is marked sensitive | the token of the call it labels |

An id is Waymarks' own number as text. Pages and sources are numbered apart, so a page and a source may share a number. Only a plain number is ever put into an address.

`AddSource` turns the facts into a Markdown list, one fact to a line. The list is sent as the source's text and as its file, `facts.md`. The intake token cannot read, so the source returned is the record Waymarks answered with and the text that was sent.

Opening a store makes no call. The first call to the instance is the first call a caller makes.

## Labels

A label is never kept between calls.

- **One page, one source, a new source.** The answer is about one space, and Waymarks marks it when that space is sensitive. The label is that mark, read in the same answer as the content.
- **The catalog, a search, the pages needing a vector.** The answer covers several spaces. With no mark, nothing in it is from a sensitive space, and every label is normal. With the mark, the adapter reads the list of spaces and labels each item by its space. It reads the list after the content. If a space is made sensitive between the two, its content reads as sensitive.
- A space the list does not hold reads as sensitive, and so does a label that is not the word `normal`.

## When Waymarks cannot be reached

Every call answers `knowledge.ErrUnavailable`, inside the timeout, when:

- nothing listens at the address, or the connection fails;
- no answer comes inside `knowledge.waymarks_timeout`;
- the answer is a redirect. None is followed, so a token is never sent on to another address;
- the answer is not Waymarks' JSON, has no `data`, or is larger than `knowledge.waymarks_reply_max_mb`;
- Waymarks refuses the token (401), or answers with a fault (500);
- Waymarks refuses a call that names no page or source (403).

Opening the store also answers `ErrUnavailable`, naming what is missing, when the address is empty or malformed or the tenant has no reader token.

When the caller's own context is done, the call returns the context's error.

The adapter retries nothing and remembers no failure. The next call tries the instance again, so a store that comes back is used at once.

No error holds a token. The address and the request are left out of an error, and anything the instance sends back is searched for the three tokens before it is put into one.

Other refusals:

| Waymarks answers | Here |
| --- | --- |
| 400 `invalid`, 413 `too_large` | `ErrInvalid`. This includes a vector of a model or a size the instance is not set up for, and any vector call on an instance that takes none. |
| 409 `stale` | `ErrStale` |
| 404, or 410 `erased`, for a page or a source | `ErrNotFound` |
| 403 for a page or a source | `ErrNotFound`: it is in a space the token does not reach |
| 403 for a search that names a space | No hits, and no error |

## Checked by

`go test ./internal/knowledge/waymarks`. The tests talk only to a fake Waymarks on this machine, served by `httptest` from `fake_test.go`. The fake answers the nine calls above as Waymarks' source says the real one does: the same addresses, JSON, grant checks, sensitive mark and errors, a refusal of an unknown field, and the page of its own rules a fresh instance holds.

- The contract suite passes with all three tokens, with every capability on and no check skipped. It passes again with the reader and intake tokens, and with the reader alone.
- An instance that is down, one that never answers, and seven kinds of answer that are not Waymarks each give `ErrUnavailable` on every call. A redirect is not followed.
- No error holds a token, even when the instance echoes one.
- Each call goes out under the token of its role.
- A sensitive space the grant does not name is not listed, found or read.
- Every option of `knowledge.adapter` opens. This test moved here from `internal/knowledge/builtin`, because this package's tests are the first to have every adapter in their build.

`verify` lists the folder in `networkAllowed`, since it imports `net/http`.

## Not proven yet

- **A real Waymarks.** The item's proof is the contract suite against a Waymarks test tenant. That needs a running instance and three tokens, which only the owner can give. Until then the adapter is proven against the fake alone, and the fake is this lane's reading of the source.
- **A chat finishes with Waymarks down.** There is no chat turn until P4-03. What is proven is the half this package owns: every call returns `ErrUnavailable` quickly and nothing panics or hangs.
- **Relevance.** A score is Waymarks' own. Recall's threshold for this adapter is `knowledge.recall_min_score_waymarks`, which ships at 0 until real scores are seen ([knowledge-recall.md](knowledge-recall.md)).

## Not here

- The server does not import the package yet, so the binary does not have `waymarks`. `cmd/keystone` belongs to the core lane.
- A turn opens the store for the person map and for recall. The `recall` and `sensitive content` checks in `verify` run turns against a small fake Waymarks of their own, in `tools/verify/waymarksfake.go`. The memory pass (P6-06) is the next caller.
- Roles in the interface. The adapter picks the token by the call. A caller that must not add sources is handed a store opened for a tenant with no intake token, or checks the flag. Chat gets the whole store, and recall and the lookups only read (decision [0213](decisions/0213-the-lookups-are-plain-values.md)).
- A wider search than 100 hits, and paging through a search. The interface has no call for either.
- The upkeep jobs' tokens (P8-06). Each job has its own token and writes pages, which the interface has no call for yet.

## Since P6-08: the embedder at work

Keystone's refresh ([embeddings.md](embeddings.md)) is the embedding client Waymarks expects: it asks `GET /v1/vectors/needed` under the embedder token, reads each page under the reader, makes the vector with the person's embedding model, and pushes it with `PUT /v1/pages/{id}/vector` under the embedder, for the revision it read. `VectorsNeeded` now takes the model's name and does not send it: the instance knows its one model, counts a vector of any other as none, and refuses one of another model or size when it is pushed (`ErrInvalid`), which stops the refresh with a note. The name pushed is the entry's `provider_model`, so the owner sets `search.embedding_model` in his instance to that name. Recall searches by meaning with `POST /v1/search/hybrid` and the vector alone; Waymarks scores those hits by their place, so `knowledge.recall_meaning_min_score_waymarks` ships at 0 and the nearest pages always join (decision [0411](decisions/0411-words-and-meaning-are-merged-by-weighted-reciprocal-rank.md)). The `embeddings` check runs it against vector routes added to the verify fake Waymarks. A real instance has not been called: that is the owner's embedder token and the live check.
