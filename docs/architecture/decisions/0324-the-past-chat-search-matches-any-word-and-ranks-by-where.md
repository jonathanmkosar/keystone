# 0324. The past-chat search matches any of the words, and ranks a chat by where they matched

November 25, 2026 · decided in P5-05

## The question

`search_chats` used the chat record's search: every word of the query in one title or one message. A model asking about an earlier chat writes several words ("lighthouse walk decision"), and with recaps there are three more places a word may be. How does the search match, and which chat comes first?

## The decision

**Any of the words.** Each word is quoted and matches as the start of a longer one, joined with OR, in the chat record's full-text tables (`chat.MatchAny`, a new function beside `Search`) and in the recaps' (`recap.Search`). Each table gives its best rows by its full-text rank, at most `chats.search_max_results`.

**Ranked by chat.** A chat's score is twice its title's best rank, plus twice its recap's, plus its best message's; lower is better and a place with no match counts nothing. So a chat that matches in its title or summary, or in more than one place, comes first, and within a table a row holding more of the words, or rarer ones, ranks higher.

**Given by chat**, up to the limit, with its recap and its best passages, after the chat the turn is in and the chats fixed rule 3 leaves out are passed over.

## Why

- Requiring every word in one message finds nothing for most of the questions a model writes. Any of the words, ranked, finds the right chat and lets rare words decide.
- A title or a recap says what a whole chat is about, so a match there means more than a word in passing.
- The full-text rank is already there, and adding ranks across tables is crude but stable: the tests and the check pin the order where it matters.

## What it costs

- A common word matches many rows; the rank keeps them low, and each table reads at most `chats.search_max_results` rows.
- Ranks from different tables are not on one scale. A real person's chats may want the weights tuned; they are in code, not settings, until there is something to tune them against.
- The person's own search on the chat screen still wants every word in one place; the two searches differ on purpose.
