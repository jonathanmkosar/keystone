# 0600. A location travels as a field of the send body and is kept beside its message, not as a block

November 27, 2026 · decided in P10-06

## The question

The owner asked for his phone's position to go with a message, so a model asked about the weather need not ask which city he is in. He chose "per message": a tap sends the location with that one message. How does the position travel from the page to the server, and where is it kept: as a new kind of content block in the message, or as data beside it?

## The decision

- **A field of the send body.** `POST /chats/{id}/turns` takes `location: {"latitude", "longitude"}` beside `text` or `blocks`, already rounded by the page. Absent, the body is exactly what it was, and so is everything the turn writes.
- **Rounded again by the server** to the person's `location.decimals` before anything is written (`Runner.checkLocation`), so the turn's stored input and every later row hold only the rounded position, whatever a client sends. A latitude past ±90, a longitude past ±180, a field a location does not have, a location on a retry (which sends no message) and any location while `location.share` is off are refused with 400 before a turn is made.
- **Kept beside the message** in `message_locations` (migration `20261127T150000_message_locations.sql`): the rounded latitude and longitude, the decimals, and the line the model was given. The row is keyed by the message and goes with it, so it goes with the chat, an incognito chat included.
- **Shown in the record** as `location` on that message (`Message.Location`), in `GET /chats/{id}`, in the stream's `message` event, and as `location` in the turn inspector's reply.

## Why

- A block kind would reach everything that reads blocks: the seam's validation, the three adapters, uploads' `Prepare`, the past-chat search, the recap, the memory pass, the share page and the import. Each would need to learn to skip it or translate it, and a forgotten one would send a provider a block it refuses. A field reaches only the turn.
- The person's words stay their words. The record's blocks are what they typed, so Copy, Edit, the past-chat search and a share link show no coordinates.
- The memory pass reads the record, not the request, so it never sees the position and cannot turn one message's location into a lasting fact.
- A row of its own, rather than a column on `turn_messages`, keeps a plain message's rows exactly as they were, so the load test's fast path (`TestTheFastPathLeavesWhatARealTurnLeaves`) still matches a real turn row for row.

## What it costs

- One more table, read with each chat's metadata (`metaOf` reads it in one query).
- A client that wants to send a location has to use the field; a location typed into the words is just words.
- The coordinates are not searchable and do not show on a share link. Neither was asked for.
