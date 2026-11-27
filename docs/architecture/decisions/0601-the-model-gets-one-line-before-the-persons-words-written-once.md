# 0601. The model gets the location as one line before the person's words, written once, in every call that sends that message

November 27, 2026 · decided in P10-06

## The question

A message sent with a location has to reach the model so that it, and its web search, can use the coordinates. Where in the request does it go, in what words, and what happens in the calls after the one that answers it? The start of every call must stay identical so providers can cache it (P5-03), and a plain message must be sent byte for byte as before.

## The decision

- **One text block at the start of that message**, before the person's words: by default "The person's approximate location: 12.35, -45.68 (rounded to about 1 km), shared with this message." The wording is the setting `location.line`, with `{latitude}`, `{longitude}` and `{distance}`.
- **In the person's message, not in the standing start.** The system blocks, the tools and the profile are untouched, so the cacheable start is the same in every call of the chat.
- **Written once**, when the turn places the message (`Runner.keepLocation`), and kept in `message_locations` with the position. Every later call that sends the message, in the same turn (tool steps) or in later turns (history), sends the same line, so the history before the current turn is the same bytes from call to call and caches as before.
- **Added where the branch becomes messages** (`buildMessages`), the one function the turn, compaction's prefix check and the inspector's previews all use, so they agree on what a call sends.
- **No reverse geocoding and no outside service.** The model gets coordinates and how far they were rounded; it and its web search can work from those.
- **Every call stays recorded**: the line is in the request the audit log keeps, and the inspector shows it in a section of its own.

## Why

- Putting the line in the standing start, or in a system block per turn, would change the start whenever a location was sent and break the cache for every later call of the chat.
- Writing it once, rather than rendering it from the setting at each call, means changing the wording later does not change the bytes of messages already sent, so a chat's history is never sent two ways.
- A separate block, rather than text joined to the person's words, keeps their words byte for byte and makes the line easy to see in the audit log.

## What it costs

- A chat whose older turns are compacted may carry the line into its summary, since the summary is made from what the calls sent. The summary is kept with the chat and goes with it.
- The inspector's preview of a turn that carried a location starts with the line rather than the person's words.
- A change to `location.line` applies to messages sent after it only.
