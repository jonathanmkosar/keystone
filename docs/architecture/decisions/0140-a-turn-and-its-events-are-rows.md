# 0140. A turn is a row, its events are rows, and a stream reads the table

November 25, 2026 · decided in P4-03

## The question

A reply streams for seconds or minutes. In that time the browser tab may close, the phone may lose its signal, and the server may stop. The plan asks for turns that survive all three, and for a client that can come back with `Last-Event-ID`. Where does a turn live while it runs?

## The decision

**A turn is a row in `turns`, in the tenant's own database, from the moment it is sent.** The row holds the body it was sent with, so it can be run later.

**Every event a turn streams is a row in `turn_events`**, numbered from 1 within the turn. That number is the event's id on the wire.

**Sending a message and reading the reply are two things.** `POST .../turns` writes the row and then reads the turn's events as any other reader does. The model call runs in a goroutine of the runner's, not in the request.

**A stream reads the table.** It sends the rows after the id it was given, waits to be woken or for `chats.stream_poll`, and reads again. The runner's wake-up is only a hint: nothing is lost if it is missed.

**A chat's turns run one at a time, oldest first.** A turn sent while another runs waits in the queue.

## Why

- Resume, replay after the end, and a second tab joining a turn are all the same query: the events after an id. None needs code of its own.
- A turn that does not live in the request cannot be ended by the request. Closing the tab stops nothing, and only the stop route stops a turn.
- The queue is the table. After a restart there is nothing to rebuild: the rows say what was waiting.
- One writer for each chat keeps the tree simple. The reply to a queued message goes under the reply before it, which is what the person meant when they typed it.

## What it costs

- A write for each delta. On SQLite in write-ahead mode that is tens of microseconds, far below the pace of any model, and it was not measured beyond the tests.
- The rows stay. A long reply is a few hundred rows, and nothing removes them but deleting the chat. Removing the deltas of old turns is a later item if the files grow.
- A reader is at most one poll interval behind when a wake-up is missed, which only happens when the events were written by another start of the server.
