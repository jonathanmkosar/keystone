# 0550. The table of chats searches by chat, with a route of its own

November 26, 2026 · decided in P4-06

## The question

The Chats screen becomes a table of every chat with a keyword search, and its results must be rows of the same table, so they can be counted, paged, filtered, sorted and selected. Keystone already has `GET /chats` (the list, newest first, with `starred`, `limit` and `offset`) and `GET /chats/search` (`chat.Search`). The item says to use the existing search and extend it only if it cannot do this. Can it?

## The decision

- **The search itself is not changed and not copied.** The same two full-text tables, the same tokenizer, and the same reading of a query (`matchQuery`: every word quoted, every word wanted in one title or one message, a word also the start of a longer one).
- **Two functions are added beside it**, in a new file of the chat record, `internal/chat/bychat.go`: `SearchByChat`, which returns every chat that matches, once each, and `Excerpts`, which cuts the marked title and snippet for the chats of one page.
- **A new route, `GET /chats/table`**, serves the table: with no `q` it is the list with its marks, and with `q` the same rows narrowed to what the search found. `GET /chats` and `GET /chats/search` stay exactly as they are.

## Why

- `chat.Search` returns hits, not chats, and at most `chats.search_max_results` of them, title hits first. Twenty messages of one chat are twenty hits. So it cannot say how many chats match, which "select all that match (with the count stated)" needs; a chat past the fiftieth hit is not found at all; and its results cannot be joined with a filter or put in another order.
- Raising its limit would not make hits into chats, and would change what `GET /chats/search` and the tools that share its limit return.
- `GET /chats` feeds the sidebar, the attach picker and the projects screen on every page load. Giving it a count of messages and seven marks for every chat would make each of those loads do the table's work, and changing its shape would touch every caller in a branch other lanes are working in.
- A snippet with the matching words marked is returned as parts, `{"text", "match"}`, not as text with markup: the screen never has to trust markup from the server, and a message that happens to hold angle brackets is shown as it is.

## What it costs

- Two routes list chats. The table's is the fuller one; the sidebar could move to it later, with `limit`.
- The search still wants every word in one title or one message. A chat with one word in its title and the other in a message is not found. That is the chat search's rule everywhere, and changing it is a change to the search, not to the table.
- A common word is counted across every message that holds it before the page is cut. Ranks are read, and no snippet is cut, for rows that are not shown.
