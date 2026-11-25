# 0092. A Waymarks label comes from the answer's own mark, and from the list of spaces read afterwards

November 25, 2026 · decided in P6-03

## The question

Every piece of content an adapter returns carries its space's label, read fresh (decision 0031). Waymarks does not put a label on a page, a source or a hit. It marks a whole answer as holding something from a sensitive space, and it lists the spaces with their labels in a call of its own. How does the adapter label each item without a moment in which a sensitive item reads as normal?

## The decision

**An answer about one space is labelled by its own mark.** A page, a source and a newly added source each come back in an answer Waymarks marks when that one space is sensitive. The label and the content are one answer.

**An answer over several spaces with no mark is all normal.** Waymarks marks the catalog when any space the caller can read is sensitive, and a search or a list of pages needing a vector when any item in it is from a sensitive space.

**An answer over several spaces with the mark is labelled from the list of spaces, read after the content.** A space the list does not hold reads as sensitive.

**Nothing is kept between calls.**

## Why

- Reading the list of spaces first leaves a gap: a space made sensitive after the list was read and before the content was would have its content labelled normal. Read the other way round, the same gap labels it sensitive. The remaining way to be wrong is a space made normal in between, whose content then reads as sensitive for one call.
- Most answers have no mark, so most calls are one request.
- A label held in the handle would let content out for as long as the handle lived (decision 0031).

## What it costs

- The adapter trusts the absence of the mark. An answer from something that is not Waymarks, with a `data` field and no mark, would read as normal. The address is the tenant's own setting, and what is there holds that tenant's own content.
- A marked answer costs a second request.
