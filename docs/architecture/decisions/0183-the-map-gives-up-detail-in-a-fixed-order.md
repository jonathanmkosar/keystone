# 0183. The map is held to a token limit by giving up detail in a fixed order

November 25, 2026 · decided in P6-04

## The question

The map must stay under a token limit whatever the size of the store. What is in it, how is it measured, and what goes first when it does not fit?

## The decision

**One line for each space: its page count, each kind of page with its count, and the first few titles of each kind.**

**It is measured by the budget's estimate for plain text**: characters over `budget.chars_per_token`, rounded up, with no margin. The caller passes that setting's value in.

**Detail is given up in this order until the text fits**: titles under each kind, one fewer at a time, down to none; then kinds in each space, fewest pages first; then spaces, fewest pages first. Each cut is counted in the text: `and 3 more kinds`, `and 4 more spaces`. If nothing fits the map is empty.

**A name from the store is put on one line and cut to a longest length.**

## Why

- The plan asks for "a short directory of what kinds of things the store holds, per space". Spaces and kinds are that directory. Titles are a bonus that makes the kinds mean something, so they go first.
- The plan's recall check speaks of pages "the person map does not name", so the map is expected to name some pages and not all.
- Cutting text at a number of characters would leave half a line and a model guessing. Cutting whole things and saying how many were cut leaves a map that is true as far as it goes.
- The package may not read settings and may not call a provider, so it cannot count tokens the way a model does. The budget's estimate is the one already used to decide whether a call fits, and it leans high (decision 0131).
- A title with a line break in it could add a line that reads as a space. Titles come from the store, and some of what is in a store came from outside.
- The same catalog gives the same text, because every order is fixed. That is what lets the map sit in a cached start.

## What it costs

- The steps are tried one at a time, each writing the whole text. For a store with thousands of kinds that is thousands of short writes on a rebuild. It happens only when the catalog changes.
- With no margin, a provider's own count may come out a little over the limit. The limit is a size for one section, and the budget still checks the whole call.
- Which titles are shown is decided by path order, not by which pages matter most. Recall, not the map, is what finds the right page.
- The kinds are the store's own words. A store that gives every page its own kind gets a map of kinds with one page each.
