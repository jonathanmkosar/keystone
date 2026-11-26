# 0511. New Keystone's events are counted in the chat's note, not recorded as steps

November 26, 2026 · decided in P12-03

## The question

The item asks for tool events "as recorded steps if the chat record can hold them, otherwise summarised in a note". New Keystone's `events` table has a row for each thing that happened in a turn: a web search, a wiki lookup, a note saved. Can they be steps in Keystone's chat record?

## The decision

No. They are **counted by kind for each chat and summarised in the chat's mark**: "New Keystone recorded 3 web searches and 2 wiki lookups here. It kept no record of what each one asked or found, so they are counted and not shown as steps." The counts are kept by New Keystone's own event type in `imported_chats.steps`; the words are the mark's `note`, shown at the top of the chat.

Events that only say a turn was queued, started and ended, which model ran and what its call was built from (`job.*`, `model.*`, `context.assembled` and the like) are not counted. The model's name is read from them and kept with the reply.

A reply's web sources are a different thing: New Keystone kept their titles and addresses with the message, so they are brought, as a list at the end of the reply.

## Why

- A step in the chat record is a tool call and its result: a name, an id, an input, and what came back, as two blocks the seam checks. New Keystone's `events` rows hold neither an input nor a result. By its own rule (`record_event`) they are "caller-sanitized operational metadata, never raw provider payloads": a search's row says "Tavily basic · 1 credit · 3 sources · 420 ms." and not what was searched for.
- A step made up from that would be a call with an invented input and an empty result. The record is sent to the model again whenever the chat is continued, so the model would read tool calls that never happened in that shape, to tools Keystone does not have under those names. Providers also check that calls and results pair up.
- A line of text in each reply ("searched the web once") would be the import's words inside the model's, would be indexed by the search under words like "search" for every chat, and would be sent back to models as if the reply had said it.
- A note on the chat is honest about what is known: that it happened, how often, and that the detail was never kept.

## What it costs

- The person cannot see which reply a search belonged to. The counts are for the chat. New Keystone's own database still says, and it is not changed.
- An event type New Keystone gains later is counted as "other" until a word is given for it in `internal/importer/plan.go`.
