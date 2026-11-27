# 0530. A search of messages cuts snippets only for what it gives, and ranks at most so many matches

November 26, 2026 · decided in P13-03

## The question

The load test (P13-03) wrote 100,000 messages for one person and searched them. A search for two common words took 2.5 seconds through the chat search route (`chat.Search`) and 1.2 to 2 seconds through the model's `search_chats` (`chat.MatchAny`); the budgets are 200 and 500 milliseconds. A search for a rare word took a few milliseconds. What made the common word slow, and what should a search do when most of what a person ever wrote matches?

## What was found

Both queries asked the full-text index for every match, joined each to its message and its chat, cut a snippet out of each, sorted all of it by rank and then kept the best fifty:

    SELECT c.id, ..., snippet(message_search, ...), ...
    FROM message_search JOIN messages m ON ... JOIN chats c ON ...
    WHERE message_search MATCH ? ORDER BY rank, m.id DESC LIMIT ?

A common word was in 87,691 of the 102,836 messages, so 87,691 snippets were cut and 175,382 rows looked up to show fifty. And ranking alone, with nothing cut, still costs about two microseconds a match, because the rank needs each message's length: 170 milliseconds for that word, and twice that at the full profile's 200,000 messages.

## The decision

In `internal/chat/ranked.go`, used by `Search` and by `MatchAnyWithin` (`MatchAny` with a bound):

1. **Only what is given is cut and read.** The index is asked for its best few in an order it keeps itself, `ORDER BY rank LIMIT n` on the index alone, so it steps to the rows asked for and no further, and the snippet is cut for those. Their messages and chats are read after, by id. Equal ranks are put newest first, as before.
2. **At most `chats.search_rank_max` matches are ranked**, 20,000 by default. A count that stops at one more than the bound says whether more match. When more do, the search is too wide for a rank to mean much, and the newest matches are given, newest first, which the index reaches from its newest end without ranking anything.
3. **In a search for any of several words** (`search_chats`), a word that more messages than the bound hold is left out of the search of messages while another of the words is in fewer. The rarer words say which chats are meant; with the common word kept, the search would be too wide, and rule 2 would give the newest messages that hold the common word and lose the old chat that holds the rare one. Titles and recaps are still searched with every word.

A bound below 1 ranks every match. A record with fewer matches than the bound, which is every small install, gets the answer it got before; `TestASearchRanksAtMostSoManyMatches` holds both.

After the change the same searches take 30 to 80 milliseconds over the same 100,000 messages.

## Why

- A person searching for "the harbour lights" means the harbour and the lights. Ranking every message that says "the" costs time in proportion to everything they ever wrote and changes nothing about which messages come first: a word in more than half the messages carries no weight in the rank at all.
- The bound is a setting, because the right number is the machine's: 20,000 matches are ranked in about 40 milliseconds on the machine the load test ran on.
- The alternative of ranking only the newest so many matches of the whole search, with no word left out, was tried on paper and refused: for "any of these words" it loses the old chat with the rare word, which is the chat a past-chat search is for.

## What it costs

- At the cut-off, which of several messages of equal rank are among the best few is the index's choice, not "the newest": the order among those given is still rank, then newest.
- A search whose every word is common gives the newest matches, not the best. Its answer says nothing about that; a person sees recent chats first, which is what the chat list shows too.
- A search for any of several words reads the index once more for each word, to count it, never past the bound.
- `MatchAny` keeps its signature and ranks everything; a caller with a large record calls `MatchAnyWithin`. The past-chat tool does.
