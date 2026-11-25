# 0212. Recall searches with the message's words, keeps by a threshold for each adapter, and fills each kind of model's room

November 25, 2026 · decided in P6-05

## The question

The plan says an automatic search on every message attaches the top hits, and that recalled items below a relevance threshold stay out. What is the search made with, what is the threshold measured on, and how many hits are read?

## The decision

**The query is the message's own words**, in lower case, each once, without single characters and without the words in `knowledge.recall_ignore_words`, cut at a word's end to `knowledge.recall_query_max_chars`. A message made of ignored words searches for nothing.

**The threshold is one setting for each adapter**, `knowledge.recall_min_score_<adapter>`, on that store's own scale. Both ship at 0, which keeps every hit the search found.

**Hits are read until each kind of model has its fill.** Up to `knowledge.recall_max_items` items are read for a model that may read sensitive content, and as many from normal spaces for one that may not, so sensitive hits ranked first do not leave the second kind short. Each item is cut to `knowledge.recall_item_max_chars` and says where to read the rest.

## Why

- The built-in store matches a page on any one word (decision 0061), and so does the fake Waymarks the adapter's tests read from Waymarks' source. The whole message as a query would find every page that holds "the". Leaving out words that say nothing of a message's subject is the plain fix, and the list is a setting a person can change.
- A score means nothing across stores. The built-in store's rank comes from its full-text index and grows with how rare a word is; Waymarks' is its own. One threshold for both would be right for neither, and the settings test fails if an adapter a person can choose has none.
- 0 is right for a small store, which is what a person starts with, and the context builder's share for recall (`chats.context_recall_percent`) bounds the room either way. A higher threshold is worth setting once a store is large; the setting's description says what the built-in store's scores look like.
- Reading for both kinds of model costs at most twice the reads, once per turn, and lets one `Found` serve the route and every call (decision 0210).

## What it costs

- A message about something the store names in other words finds nothing until search by meaning (P6-08).
- With a threshold of 0, a word shared by chance brings a page in. Its cost is bounded by the item limit, the item size and the builder's share.
- The ignore list is English. A person who writes in another language sets their own.
