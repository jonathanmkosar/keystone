# 0372. An incognito chat is deleted when the person leaves it or once it has been idle, and its calls stay in the audit log

November 25, 2026 · decided in P6-07

## The question

The plan's incognito chats "keep no record and run no memory pass". P6-06 made incognito a flag that stops the memory pass (decision 0284) and left the rest here. When does an incognito chat's record go, what goes with it, and what stays, given that every model call is recorded (fixed rule 4)?

## The decision

**Incognito from the start.** `POST /chats/incognito` makes a chat with the flag set before its first message, and the new-chat screen has a switch for it. The flag (`memory_chats`) stays the memory package's.

**Nothing of it is used elsewhere while it is open.** It runs no memory pass (as before), gets no recap (`POST /chats/{id}/recap` answers 409, and none is made after its turns), and `search_chats` and `read_chat` from any other chat leave it out. From inside itself it can still be read.

**Leaving deletes it.** The screen calls `POST /chats/{id}/close` when the person leaves the chat's screen, and sends it with the browser's beacon when the page goes. The chat is deleted as a deleted chat is, with its messages and their search rows, its turns and their events, its recall and context records, its pins and summaries; a turn still running is stopped first. A chat that is not incognito is refused, so a mistake in the screen cannot delete an ordinary chat.

**Or the time limit.** A sweep, at start and every `knowledge.incognito_sweep_every` (install, 1m), deletes each incognito chat that was left, and each one whose last message is older than the person's `knowledge.incognito_keep` (tenant, 24h, from 5m to 720h). A turn still waiting or running keeps an idle chat that was not left.

**Tasks.** A background task from an incognito chat works in an incognito chat (P8-01). Closing a chat closes the chats of the tasks it started. A chat a task still works in, or one a task that has not ended was started from, is kept until the task ends, and then goes, so leaving never cuts a task short. A scheduled task marked incognito makes incognito chats, which go the same way.

**What stays.** The audit log keeps the chat's model and tool calls, what was sent and what came back, because fixed rule 4 says every call is recorded and no setting switches it off; the log is append-only and read by no model and no search. The usage rows keep tokens and cost and hold no words. The deleting is not itself a record of what was said: the API wrapper records the route, not a body.

## Why

- "Keeps no record" read as the chat record keeping nothing once the person is done, which is how a person thinks of a chat they leave.
- Deleting through the chat record's own delete reaches every table that refers to a chat, now and later, as `ON DELETE CASCADE` does.
- A time limit covers a tab that was closed or a browser that never sent the beacon. A day is long enough not to delete a chat under a person who stepped away, and short enough that it does not linger.
- Fixed rule 4 cannot be weakened, so the audit log is where incognito ends, and the docs and the setting's description say so.

## What it costs

- An incognito chat opened from a schedule's run, or a task's chat, is deleted when the person reads it and leaves; a link to it then finds nothing.
- Reloading an incognito chat's page is leaving it.
- The audit log, and a backup made while the chat existed, still hold its words. An owner question asks whether that is what he wants of incognito.
