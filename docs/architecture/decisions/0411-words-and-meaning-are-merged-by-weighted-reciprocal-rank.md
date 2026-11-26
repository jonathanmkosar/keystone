# 0411. Recall searches by words and by meaning apart, and merges the two lists by weighted reciprocal rank, each list held to its own threshold

November 25, 2026 · decided in P6-08

## The question

With a query vector, recall can search by meaning as well as by words. The stores can already take both at once: the built-in store adds a cosine to a word score, and Waymarks' hybrid search joins its two lists by reciprocal rank with k = 60. A word score (bm25 here, Waymarks' own there) and a cosine are on different scales, and Waymarks gives a hit found by vector a score that is its place, not its nearness. How are the two searches joined, how is a weak hit by meaning kept out, and what does the person see?

## The decision

**Two searches, one merge in Keystone.** `recall.Find` searches by words as before, then by the vector alone (`knowledge.recall_meaning_max_results` pages), and merges the lists itself. The same merge then holds for every store, and the inspector can say which search found each hit.

**Weighted reciprocal rank.** A hit scores `weight / (k + p)` in each list it passes, where `p` is its place among the hits of that list that pass, and the scores are summed. The weights are `knowledge.recall_merge_words_weight` and `knowledge.recall_merge_meaning_weight` (1 and 1), and `k` is `knowledge.recall_merge_rank_constant` (60, Waymarks' own). Places are merged and never scores, because the scales differ. Ties go to the better place among the words, then among the meaning's, then a page before a source and the lower id.

**Each list keeps its own threshold on its own scale.** The words' is `knowledge.recall_min_score_<adapter>`, as before. The meaning's is `knowledge.recall_meaning_min_score_<adapter>`: a cosine of 0.3 for the built-in store, and 0 for Waymarks, whose search by vector gives places. A hit that passes neither is below the threshold; one that passes either is merged.

**What did not change.** With no vector, recall is the words' list exactly as before, record and all. A hit found by meaning keeps the label its store gave it, a hit both found is sensitive when either said so, and `Found.For` filters the merged list as it filtered the words'.

**What the person sees.** The record gains the meaning's model, its count and threshold, a note when there could have been a vector and was not, and for each hit `found_by`, its meaning score and its merged score.

## Why

- Reciprocal rank is what Waymarks chose for the same problem, needs no common scale, and is hard to break with one large score.
- Separate thresholds keep a weak cosine from riding on a strong word score, and keep the words' threshold meaning what it meant.
- Merging in Keystone, not in the store, gives one behaviour for both stores and a record that says why each hit is there.

## What it costs

- Two searches where one would do, for a store that could join them itself.
- With Waymarks the meaning's threshold cannot drop a far page, so its nearest pages join every message, up to `knowledge.recall_meaning_max_results`. A message about nothing is checked on the built-in store alone. An owner question asks Waymarks for a nearness on hybrid hits.
- 0.3 is a guess for a real model's cosines, to tune once one is in use.
