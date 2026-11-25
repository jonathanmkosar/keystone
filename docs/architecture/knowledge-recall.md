# Recall

Folder: `internal/knowledge/recall`, joined to a turn by `internal/turn/recall.go`

Recall brings what a person's knowledge store holds into a chat. With each message the store is searched, the hits at or above a relevance threshold are read, and each model call is given them, each labelled with where it came from so the model can cite it. A standing rule tells the model to look in the store before it says it does not know. Three lookups let a model go deeper: search the store, read a page, read a source.

Content from a sensitive space reaches only a model that may read it. That is fixed rule 3, and recall enforces it twice: what a call is given is filtered for the models it may reach, and a call that is given sensitive content is routed to no model that may not read it, first or as a fallback.

The package imports the knowledge interface ([knowledge.md](knowledge.md)) and the tenant handle, and nothing else of Keystone's. It reads no setting and opens no connection. The caller reads the settings and passes them in.

Decisions: [0210](decisions/0210-recall-searches-once-before-the-route.md) for when the search is made and who a call is built for, [0211](decisions/0211-a-chat-sent-sensitive-content-is-held.md) for the chat that was sent sensitive content, [0212](decisions/0212-what-recall-searches-with-and-keeps.md) for the query, the threshold and the limits, [0213](decisions/0213-the-lookups-are-plain-values.md) for the lookups, and [0214](decisions/0214-the-search-first-rule-is-a-standing-instruction.md) for the search-first rule.

## Automatic recall

```go
found, err := recall.Find(ctx, knowledgeStore, message, options)
out := found.For(model.MayReadSensitive())
```

`Find` searches the store once, by words, with the person's newest message, and reads each hit worth keeping:

1. **The query.** The message's words, in lower case, each once, without single characters and without the words in `knowledge.recall_ignore_words` ("the", "what", "please" and the like), cut at a word's end to `knowledge.recall_query_max_chars`. A store matches a page on any one word, so a word such as "the" would find every page. A message made of such words alone searches for nothing.
2. **The search.** `Store.Search` with that text and a limit of `knowledge.recall_search_max_results`. No query vector yet: that is P6-08.
3. **The threshold.** A hit that scores under the threshold is left out. A score is on the store's own scale, so each adapter has its own: `knowledge.recall_min_score_built_in` and `knowledge.recall_min_score_waymarks`. Both ship at 0, which keeps everything the search found.
4. **The read.** Each hit over the threshold is read with `ReadPage` or `ReadSource` and cut to `knowledge.recall_item_max_chars`. Hits are read in order until both kinds of model have their fill of `knowledge.recall_max_items`, so a model that may not read sensitive content is not left short because sensitive hits came first. A hit that cannot be read is passed over.

An item's text starts with a line that names it:

    From the person's knowledge store, found by searching for their message. Page "Runner beans" in the space "garden", page id 7:
    Runner beans go in the north bed after the last frost. ...
    [Cut here. The rest of page 7 can be read with a knowledge lookup.]

The last line is there only when the text was cut. A source's line also gives its date. The item's label, for the turn inspector, is `garden: Runner beans (page 7)`.

A nil store, an empty query and a limit of no items find nothing and are not errors. An error is the store's own or the context's. The caller logs it and the chat carries on with nothing recalled.

### For one model

`Found` exports nothing. `Found.For(mayReadSensitive bool)` is the only way to what was read, and returns an `Outcome`:

| Field | What it holds |
| --- | --- |
| `Items` | The items for this model, the most relevant first, each a `Label` and a `Text`. |
| `Sensitive` | An item is from a sensitive space. The call that carries them must be routed as sensitive. |
| `Record` | What was found, kept and dropped, and why, for the turn inspector. |

With `false`, every hit from a sensitive space is left out, and the record counts it as `Withheld`. A hit is sensitive when the search said so or the read said so, and a label that is blank or unknown reads as sensitive (decision [0031](decisions/0031-label-reads-as-sensitive.md)). A page made sensitive between the search and the read is sensitive.

### The record

`Record` is what the turn inspector (P5-04) shows: the adapter, the query, the threshold, how many hits were found, kept, dropped and withheld, whether a kept item was sensitive, a note when nothing was searched, and each hit with its type, id, space, title, score, whether it was kept, and if not, why:

| Reason | Means |
| --- | --- |
| `below_threshold` | It scored under the relevance threshold. |
| `sensitive` | It is from a sensitive space and the call's models may not read it. |
| `over_limit` | Enough better hits were kept already. |
| `unreadable` | The page or source could not be read. |

The record names sensitive pages, so it is shown to the person and never sent to a model.

## Fixed rule 3, twice

1. **What a call is given.** `Found.For` and `Lookups` take a plain yes or no, `Model.MayReadSensitive()` from the registry ([models.md](models.md)), and there is no other way to the content. The package is a guard of `rules.sensitive_content` in `tools/verify/fixedrules.go`: `verify` fails if it ever imports `settings` or `internal/tenantsettings`, itself or through another package.
2. **Where a call may go.** When a kept item is sensitive, the turn tells routing the call is sensitive, and routing keeps only models that may read it, in the route and in the fallback chain ([routing.md](routing.md)).

### In a turn

`internal/turn/recall.go` does this, in the order a turn runs:

1. **Search, before the route.** `searchKnowledge` reads the person's `knowledge.adapter`. With `none`, nothing is opened, searched or recorded. Otherwise it opens the store, and with `knowledge.recall` on it calls `Find` with the person's newest message (for a retry, the message the old reply answered) inside `knowledge.recall_timeout`. The store is closed when the search ends. A store that does not open, a search that fails or runs out of time, and a setting that does not read each give nothing, a note in the record and one line in the log. The turn goes on.
2. **The route.** `routed` in `route.go` asks `sensitive`: the call is sensitive when the chat was sent sensitive content before (decision [0211](decisions/0211-a-chat-sent-sensitive-content-is-held.md)), or when the chat's model may read sensitive content and `For(true)` kept a sensitive item. A chat whose record cannot be read counts as sensitive. Routing then drops every model that may not read it, and with none left the turn ends with the code `sensitive`.
3. **Each model call.** A call is built once and sent as it is to whichever model of the route answers. So `build` in `context.go` builds it for the least of them: `readsSensitive` is true only when every model of the route may read sensitive content. The person map is made for that answer too, and recall gives `For(that answer)` to the context builder's `recall` slot ([context.md](context.md)).
4. **The record.** The first call of the turn keeps the record in `turn_recall`, with the adapter and the note. When a kept item is sensitive the row marks the chat as held. If the row cannot be written, nothing from a sensitive space is sent in that turn, since the chat could not be held.

The test tenant has no route: its one model is local, and it is given what a local model may read.

So a sensitive item reaches a call only when every model the call may reach may read it, and a call routed as sensitive cannot fall back to one that may not. The map follows the same answer: a local model with a hosted model behind it is given the open map.

## The search-first rule

When the person's store opened for the turn, the text of `knowledge.search_first_text` is added to the standing instructions, the context builder's `rules` part, as a paragraph after `chats.context_system_text`. It tells the model to look in the person's store before it answers anything about their world or says it does not know, to read a page or source in full with the lookups when they are offered, and to say where an answer came from. With no store, or a store that did not open, it is left out. An empty setting sends none.

It sits in the part every call starts with, which does not change from one call to the next, so a provider can cache it (decision [0214](decisions/0214-the-search-first-rule-is-a-standing-instruction.md)).

## The lookups

```go
list, err := recall.Lookups(mayReadSensitive, recall.LookupOptions{MaxResults: 10, MaxChars: 6000})
```

Three plain values, each a `Lookup`:

| Name | Input | Returns |
| --- | --- | --- |
| `knowledge_search` | `query`, and `space` and `limit` if wanted | The matching pages and sources, best first, each with its type, id, title, space and a snippet |
| `knowledge_read_page` | `id`, and `from` to read on | The page's heading line, the sources it cites, and its text from `from`, in parts of `MaxChars` |
| `knowledge_read_source` | `id`, and `from` to read on | The source's heading line, where it came from, and its text, in parts |

Each has a `Name`, a `Description` written for the model, a JSON Schema in `Schema`, `ReadOnly` true, and `Run(ctx, tenantStore, knowledgeStore, input)`, which returns a `Result`: the `Text` for the model, whether it holds sensitive content, and how many results were withheld. An input that does not read, or that has a field the lookup does not know, is an error wrapping `ErrInput`, and its message is written for the model. `ErrNoStore` means no store was passed.

The lookups obey the same filter as recall, with the same plain answer. For a model that may not read sensitive content, a search leaves sensitive hits out and says how many, and a read of a sensitive page or source says only that this model may not read it, with no title and no space. The answer to pass is the least of the call's route, as above.

No registry is built here. The turn offers the lookups as tools (`internal/turn/lookups.go`): a call that offers tools, for a person whose store opened, gets the three lookups made for the least of its route by `Runner.knowledgeLookups`, and the calls the model makes in reply run those same ones, through a registry of their own so they are recorded, timed and checked like every tool. Each lookup opens the store for itself. A result marked `Sensitive` holds the chat with `recall.Hold` before the model sees it, and is not given when the hold cannot be kept.

The lookups and recall only read: nothing here calls `AddSource`. The store a turn opens is the whole store, and no view of it that cannot add is made (decision [0213](decisions/0213-the-lookups-are-plain-values.md)).

## The table

`turn_recall`, from the migration `20261125T090000_turn_recall.sql`: one row for each turn of a person who has a store, and for each turn a lookup sent sensitive content in. It holds the turn, its chat, whether the turn sent sensitive content, and the record as JSON. The rows go when the chat is deleted.

| Function | Does |
| --- | --- |
| `Save(ctx, ts, turnID, chatID, record)` | Keeps a turn's record. Saving again replaces it, and never takes back the sensitive mark. |
| `Hold(ctx, ts, turnID, chatID)` | Marks a turn as having sent sensitive content, with no record of its own. For the tool loop. |
| `Saved(ctx, ts, turnID)` | Reads a turn's record back, for the inspector. |
| `Held(ctx, ts, chatID)` | Whether any turn of the chat sent sensitive content. |

## Settings

In `settings/knowledge.json`. The package exports each key as a constant, and `SettingMinScore(adapter)` gives an adapter's threshold.

| Setting | Default | Scope | What it sets |
| --- | --- | --- | --- |
| `knowledge.recall` | true | tenant | Whether each message is searched for. With no store nothing is either way. |
| `knowledge.recall_timeout` | 3s | tenant | How long recall may take before a reply starts without it, from 100ms to 1m. |
| `knowledge.recall_min_score_built_in` | 0 | tenant | The relevance threshold for the built-in store, on its own scale. |
| `knowledge.recall_min_score_waymarks` | 0 | tenant | The relevance threshold for Waymarks, on its own scale. |
| `knowledge.recall_ignore_words` | about a hundred words | tenant | Words left out of the query. |
| `knowledge.recall_search_max_results` | 20 | tenant | The most hits asked for, from 1 to 100. |
| `knowledge.recall_max_items` | 5 | tenant | The most items one call is given, from 0 to 50. 0 recalls nothing. |
| `knowledge.recall_item_max_chars` | 1500 | tenant | The most of one page or source an item holds, from 100 to 100000. |
| `knowledge.recall_query_max_chars` | 500 | install | The most of the message searched with, from 1 to 10000. |
| `knowledge.search_first_text` | four sentences | tenant | The search-first rule. Empty sends none. |
| `knowledge.lookup_max_results` | 10 | tenant | The most results one search by the model returns, from 1 to 100. |
| `knowledge.lookup_max_chars` | 6000 | tenant | The most text one lookup returns, from 200 to 200000. |

`CheckOptions` and `CheckLookupOptions` refuse a value out of range with `ErrOptions` and name the setting. The share of a call recall may take is the context builder's `chats.context_recall_percent`.

## Checked by

`go test ./internal/knowledge/recall`, against the built-in store with three invented spaces and a small store written in the test:

- A message about a page brings it first, with the line that says where it came from. A message about nothing brings nothing, and so does one made of ignored words.
- A model that may not read sensitive content is given nothing from the sensitive space, and the record says how many hits were withheld. One that may is given the page and the source, and the outcome is marked sensitive. A space made sensitive is withheld at the next search.
- A blank label on the hit, a blank label on the page, a page made sensitive after the search and an unknown label each read as sensitive.
- The threshold, the limit on items, the size of an item and the size of the query hold, each kind of model gets its own best few, and each hit left out says why.
- No store, `none`, a store that is down and a context that is done each give nothing.
- The lookups are ready to register, obey the filter, read a long page in parts, refuse bad input, and say when there is no store.
- A record is kept and read back, and a chat that was sent sensitive content stays held, and its rows go with it.

`go test ./internal/turn`: a message about a page puts it in the call with the search-first rule and keeps a record, a message about nothing recalls nothing, and `none` searches nothing; a local model that recalled sensitive content does not fall back to a hosted one, a held chat on a hosted model ends with the code `sensitive`, and a plain question that falls back to a hosted model is given the open map; a store that does not open, or never answers, does not stop a chat, and recall gives up at its own timeout; the answer for a call is the least of its route's, and the lookups follow it.

`verify` ([tools.md](tools.md)), with the real server on a throwaway folder, the invented knowledge in `fixtures/test-tenant/knowledge/knowledge.json`, and both stores, the built-in one and the Waymarks adapter against a fake Waymarks on this machine:

- `recall`: on the test tenant, every question of the fixture brings its answer into the call, from pages the person map does not name and from a source; the share answered may not fall below the fixture's floor, now 1; every call holds the search-first rule; a message about nothing recalls nothing; `none` searches nothing.
- `sensitive content`: content from the sensitive space never appears in a call to a model that may not read it, by recall, by a lookup or by a fallback, and the held chat is not sent to a hosted model. It runs on an invented tenant beside the test tenant, with a hosted, a local and a trusted model, because the test tenant's one model is local.

## Not here yet

- **Search by meaning.** Recall searches by words. The query vector is P6-08.
- **The inspector.** The records are kept and `recall.Saved` reads them. The route and the screen are P5-04, which can also offer pinning or removing an item for the next turn.
- **A threshold tuned on real scores.** Both ship at 0. The Waymarks adapter has met only a fake.
- **The share over time.** The recall check compares the share with a floor in the fixture. Nothing keeps the share of each run.
- **A held chat on another branch.** A chat is held as a whole, even when the person edits back to a point before the sensitive turn.
- **The inspector (P5-04) is built.** `GET /chats/{id}/turns/{turn}/inspect` returns the turn's record from `recall.Saved`, and each recalled item in a call carries a `ref` made from its hit (`page:7`, `source:3`), so a person can take it out of the chat's next turn. Taking an item out never changes where a call goes: the route and the chat's mark are decided from what `Find` found, as before ([inspector.md](inspector.md), decision [0272](decisions/0272-a-removed-item-is-marked-and-recorded-and-never-moves-a-call.md)). `internal/turn/recall.go` changed in one place: `recallFor` gives each item its `ref`.
- **The chat-reading tools (P5-05) follow the hold.** `search_chats` and `read_chat` leave out a chat that `Held` marks, or whose recap was made while it was, for a call whose route may reach a model that may not read sensitive content, decided for the least of the route as the lookups are; a call whose every model may read it is given the chat, and its own chat is held first with `Hold`. A held chat's recap is written only by a model that may read it, and kept as sensitive ([chat-summaries.md](chat-summaries.md)).
