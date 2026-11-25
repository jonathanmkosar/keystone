# 0061. A built-in search matches any word, with stemming, and adds a cosine to the word score

November 24, 2026 · decided in P6-02

## The question

`Search` takes "plain words, not a query language". Must a page hold every word, or any of them? And when a query carries words and a vector, how do the two scores become one?

## The decision

**Any word.** A page or a source is found when it holds at least one word of the query. Each word is quoted before it reaches FTS5 and the words are joined with `OR`, so nothing a person or a model typed is read as search syntax.

**Stemmed.** The index uses FTS5's `porter` tokenizer over `unicode61` with diacritics removed. `egg` finds `eggs` and `pruned` finds `prune`.

**The word score is bm25, turned over.** SQLite's bm25 is lower for a better match. The store returns its negative, so higher is better: zero or more, with no upper end.

**The vector score is the cosine**, for pages whose vector is of the query's model and size. A cosine of zero or less is not a hit.

**Both together add.** A page found by the words and by the vector scores the sum of the two.

## Why

- Recall (P6-05) searches with the person's whole message. Almost no page holds every word of a sentence, so "every word" would find nothing and recall would be silent. With "any word", bm25 puts the pages that hold more of the words, and the rarer ones, first, and recall's relevance threshold drops the rest.
- The chat record's search (P4-02) asks for every word and matches prefixes, because there a person types a few words into a search box. This is a different caller, so it is a different rule.
- Stemming does for a sentence what prefix matching does for a search box, without `a` matching every word that starts with it. It is an English stemmer. Words in other languages are matched whole.
- The sum is the simplest thing that keeps "what either finds is found" and "best first". It is what the suite's own reference store does.

## What it costs

- A common word in the query finds many pages, each with a score near zero. A caller that shows hits needs a threshold, which the interface already says is set for each adapter.
- bm25 has no upper end and a cosine stops at 1, so in a search with both, a strong word match outweighs any vector. How to weigh them is not settled. It is left until recall runs on the test tenant with a real embedding model (P6-05, P6-08), and a weight would then be a setting.
- The stemmer sometimes joins words that are not the same, such as `organ` and `organic`.
