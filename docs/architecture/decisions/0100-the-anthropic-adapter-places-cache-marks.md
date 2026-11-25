# 0100. The Anthropic adapter places the cache marks itself, until the seam has a marker

November 24, 2026 · decided in P3-04

## The question

The Messages API caches the start of a request only where a block is marked with `cache_control`. The item's scope includes cache control. The seam's blocks have no marker: `seam.md` leaves it to caching, P5-03. Three other adapters and lanes were being built at the same time, and a new field on `seam.Block` would have changed a type they all use.

## The decision

**The adapter decides where the marks go.** With the setting `anthropic.cache_control` on, it marks two places:

1. The last system block, or the last tool when there are no system blocks. The API reads tools, then system, then messages, and a mark covers everything before it.
2. The last block of the last message that is not a thinking block.

**The seam is not changed.** When P5-03 gives the seam a marker, the adapter sends marks where the seam says, and these two become what it does for a request that has none.

## Why

- The plan keeps the start of every call identical, rules, tool names, profile, person map and project, so providers can cache it. That start is the tools and the system blocks, and the first mark covers exactly that.
- The second mark is the API's usual way to cache a conversation: each turn writes up to its last message, and the next turn reads it.
- Two marks stay well inside the four the API allows, which leaves room for the context builder to add its own.
- A prefix too short to cache is not an error. The API ignores the mark.
- A change to `seam.Block` belongs with the item that decides what a marker means for all three adapters, not with one of them.

## What it costs

- The second mark writes the cache on every turn, at the API's higher price for a write, including for a chat that never gets a second turn. The setting turns both marks off. If the cost shows in the usage records, the second mark can become a choice of its own.
- The adapter cannot know that part of the system text changes on every call, such as the time. Text like that should come after the stable part, which is the context builder's job (P5-01).
- A gateway that refuses the field needs the setting off for the whole install, since the setting is not per model.
