# Chats and turns in the API

Folder: `internal/turn`

The routes a chat screen calls, and the runner that takes a person's message through the model seam and streams the reply. The routes sit in the API shell ([api.md](api.md)): each needs a session, gets that session's tenant store and no other, and is recorded in the audit log. The chat record underneath is [chat.md](chat.md), and the content of a message is the model seam's format ([seam.md](seam.md)).

The first half of this page is the contract the chat screen builds against. `docs/api/openapi.json` has the same shapes, built from the Go types in `internal/turn/routes.go`.

## The routes

Every path starts with `/api/v1`. `{id}` is a chat's id, sixteen hex characters. `{turn}` is a turn's id, the same shape.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /chats` | Query: `starred=true`, `limit`, `offset`. | `ListReply`: `chats` (each a `Chat`), newest message first, and `more`. |
| `POST /chats` | No body. | `ChatReply`, 201. The new chat has no title and no messages. |
| `GET /chats/search` | Query: `q`. | `SearchReply`: `query` and `hits`. |
| `GET /chats/{id}` | | `ChatReply`: `chat`, `messages` (the current branch), `model`, `turns` (queued or running). |
| `PATCH /chats/{id}` | `PatchBody`: `title`, `starred`, either or both. | `Chat`. |
| `DELETE /chats/{id}` | | `DeletedReply`: `{"deleted": true}`. |
| `POST /chats/{id}/select` | `SelectBody`: `message_id`. | `ChatReply`, with the branch that message is on. |
| `GET /chats/{id}/model` | | `ModelChoice`. |
| `POST /chats/{id}/turns` | `SendBody`: `text` or `blocks`, and optionally `edit_of`, `retry_of`, `model`. | A stream of events. The turn's id is in the `X-Turn-Id` header. |
| `GET /chats/{id}/turns/{turn}` | | `Turn`. |
| `GET /chats/{id}/turns/{turn}/events` | Header `Last-Event-ID`, or query `after`. | The same stream, from the start or from after that event. |
| `POST /chats/{id}/turns/{turn}/stop` | No body. | `Turn`, as it stands after the stop. |

### The shapes

**`Chat`**

| Field | Meaning |
| --- | --- |
| `id` | The chat's id. |
| `title` | Empty until the chat has one. The first reply gives it one. |
| `starred` | |
| `created`, `active` | When it was made, and when its last message was written. |
| `busy` | A turn is queued or running in the chat. |

**`Message`**

| Field | Meaning |
| --- | --- |
| `id` | A number, never used twice. A later message has a larger one. |
| `chat_id` | |
| `parent_id` | The message this one answers or follows. Absent on a first message. |
| `role` | `user` or `assistant`. |
| `blocks` | The content, as seam blocks: `text`, `thinking`, `tool_call`, `tool_result`, `image`, `file`. |
| `created` | |
| `siblings` | The ids of every message with the same parent, this one included, oldest first. More than one means a "2 of 3" switcher belongs here. Step with `POST /chats/{id}/select`. |
| `turn_id` | The turn that wrote it. Absent on a message no turn wrote. |
| `model` | On an assistant message: the registry name of the model that wrote it. |
| `stop_reason` | On an assistant message: `end`, `tool_call`, `max_tokens` or `refused` from the model, `stopped` when the person stopped it, `failed` when the call broke off. |
| `usage`, `cost_micros` | On an assistant message: the token count of the call that wrote it, and its cost in millionths of a US dollar. |

**`ChatReply`**: `chat`, `messages`, `model` (a `ModelChoice`) and `turns`. `turns` holds the chat's turns that have not ended, oldest first. A screen that opens a chat and finds one reads its events to join the stream.

**`ModelChoice`**

| Field | Meaning |
| --- | --- |
| `model` | The model the next turn uses when the turn names none: a `ModelInfo`. Absent when there is none. |
| `source` | Where it came from: `chat` (the chat's last turn used it), `default` (the `chats.default_model` setting), `first` (the first usable model in the registry), `test` (the test tenant's recorded turns), `none`. |
| `reason` | Why there is no model, for a person. Empty otherwise. |
| `models` | The models a turn in this chat may name, each a `ModelInfo`. |

**`ModelInfo`**: `name` (what a turn is sent with), `label` (what a picker shows), `class` (`local`, `hosted` or `trusted`), `capabilities`, `context_window`, `output_limit`, and `test`, true for the fake provider. It never holds an address or the name of a key.

**`SendBody`**

| Field | Meaning |
| --- | --- |
| `text` | The message. |
| `blocks` | The message as seam blocks, for one that is more than text. Give `text` or `blocks`, not both. |
| `edit_of` | The id of an earlier message of the person's that this one replaces. The new message becomes a branch beside it. |
| `retry_of` | The id of an assistant message to make again. Send no `text` and no `blocks`: no message is written, and the new reply becomes a branch beside the old one. |
| `model` | The registry name of the model to answer. Absent means the chat's own. |

**`Turn`**

| Field | Meaning |
| --- | --- |
| `id`, `chat_id` | |
| `status` | `queued`, `running`, `done`, `stopped` or `failed`. |
| `model` | The model that answers. Set when the turn leaves the queue. |
| `user_message_id` | The person's message, once it is in the record. |
| `reply_message_id` | The last assistant message the turn wrote. |
| `stop_reason` | The model's last stop reason. |
| `error` | On a failed turn, a `TurnError`: `code`, `message`, `retryable`. |
| `usage`, `cost_micros` | The sum over the turn's model calls. |
| `created`, `started`, `ended` | |
| `last_event_id` | The id of the newest event the turn has streamed. |

**`SearchReply`**: `hits` are the chat record's: `chat_id`, `title`, `message_id` and `role` (absent when the title matched), `snippet` and `when`. Open a hit on a branch that is not current with `POST /chats/{id}/select`.

### Errors

A route that fails before a stream opens answers with the API's error body. The codes these routes add to the shell's:

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | There is no such chat, message or turn in this person's record. |
| `bad_request` | 400 | The body is refused. `error` says why: an empty message, a title that is too long, `edit_of` that names a reply. |
| `queue_full` | 429 | The chat already has `chats.turn_queue_max` turns waiting. |
| `not_built` | 501 | The route is in the contract and not built yet. |

Once a stream is open the status is 200, and a failure is an `error` event.

## The stream

`POST /chats/{id}/turns` and `GET /chats/{id}/turns/{turn}/events` answer with server-sent events. Each event has an `id`, a name and one line of JSON.

- **Ids count from 1** within a turn, with no gaps. A client that lost its connection calls the `events` route with the last id it saw, in `Last-Event-ID` or in `after`, and gets every event after it. With neither it gets the turn from its first event. This works while the turn runs and after it has ended.
- **`end` is always the last event**, and the server closes the stream after it. A client that sees the stream close without an `end` lost its connection, and resumes.
- **The turn goes on without the client.** Closing the stream does not stop the turn. Only the stop route does.

| Event | Data | When |
| --- | --- | --- |
| `turn` | `Turn` | First, with the status `queued`. Again, with `running` and the model, when the turn leaves the queue. |
| `message` | `Message` | A message was written to the record: the person's own at the start, each assistant message when it is whole, each set of tool results. It replaces whatever the screen built from the deltas for that message. |
| `start` | `step`, `model` | A model call opens. `step` counts the turn's model calls from 1. |
| `thinking` | `step`, `index`, `text` | More of a thinking block. |
| `text` | `step`, `index`, `text` | More of a text block. |
| `tool_call` | `step`, `index`, `id`, `name`, `input` | The model asks for a tool. Sent once, when the call is whole. `input` is a JSON object. |
| `tool_result` | `id`, `is_error`, `content` | What the tool gave back. `id` is the call's. |
| `usage` | `step`, `model`, `stop_reason`, `usage`, `cost_micros` | One model call ended. |
| `title` | `chat_id`, `title` | The chat was given its automatic title. Only on a chat's first reply. |
| `error` | `code`, `message`, `retryable` | The turn failed. An `end` with the status `failed` follows. |
| `end` | `Turn` | The turn as it ended: `done`, `stopped` or `failed`. |

`index` is a block's place in one step's reply, from zero. A `text` or `thinking` event with an index the screen has not seen opens a new block. Blocks arrive one at a time, in order.

The codes of an `error` event:

| Code | Meaning |
| --- | --- |
| `no_model` | The person has no model a turn can use. |
| `model_unavailable` | The model cannot be opened: its adapter is not in this build, its key is not in the vault, or its entry is refused. |
| `too_long` | The chat does not fit the model's context window. |
| `auth`, `rate_limited`, `overloaded`, `invalid`, `unavailable`, `other` | The provider's failure, by the seam's kinds. |
| `step_limit` | The model asked for tools more times than `chats.turn_max_steps`. |
| `interrupted` | Keystone stopped while the turn was running. |
| `failed` | The turn failed inside Keystone. The reason is in the server's log. |

### One turn, as it streams

The test tenant sends "Suggest three names for a small bakery by the harbour." as the first message of a new chat:

    POST /api/v1/chats/4be0c1a2d3e4f506/turns
    {"text": "Suggest three names for a small bakery by the harbour."}

The answer has the status 200, the header `X-Turn-Id: 9a1f0c2b3d4e5f60`, and this stream. Long values are cut short with `…`, and the comment lines of the heartbeat are left out.

    id: 1
    event: turn
    data: {"id":"9a1f0c2b3d4e5f60","chat_id":"4be0c1a2d3e4f506","status":"queued","usage":{"input_tokens":0,"output_tokens":0},"cost_micros":0,"created":"2026-11-25T09:14:03.120Z","last_event_id":1}

    id: 2
    event: turn
    data: {"id":"9a1f0c2b3d4e5f60","chat_id":"4be0c1a2d3e4f506","status":"running","model":"recorded-turns","usage":{"input_tokens":0,"output_tokens":0},"cost_micros":0,"created":"2026-11-25T09:14:03.120Z","started":"2026-11-25T09:14:03.131Z","last_event_id":2}

    id: 3
    event: message
    data: {"id":41,"chat_id":"4be0c1a2d3e4f506","role":"user","blocks":[{"type":"text","text":"Suggest three names for a small bakery by the harbour."}],"created":"2026-11-25T09:14:03.133Z","siblings":[41],"turn_id":"9a1f0c2b3d4e5f60"}

    id: 4
    event: start
    data: {"step":1,"model":"recorded-turns"}

    id: 5
    event: thinking
    data: {"step":1,"index":0,"text":"A small bake"}

    id: 6
    event: thinking
    data: {"step":1,"index":0,"text":"ry by a harb"}

    …

    id: 14
    event: text
    data: {"step":1,"index":1,"text":"Three names:"}

    …

    id: 27
    event: message
    data: {"id":42,"chat_id":"4be0c1a2d3e4f506","parent_id":41,"role":"assistant","blocks":[{"type":"thinking","text":"A small bakery by a harbour. …","signature":"sig-test-0101"},{"type":"text","text":"Three names:\n\n1. **Tide and Crumb** …"}],"created":"2026-11-25T09:14:03.702Z","siblings":[42],"turn_id":"9a1f0c2b3d4e5f60","model":"recorded-turns","stop_reason":"end","usage":{"input_tokens":24,"output_tokens":52,"thinking_tokens":24}}

    id: 28
    event: usage
    data: {"step":1,"model":"recorded-turns","stop_reason":"end","usage":{"input_tokens":24,"output_tokens":52,"thinking_tokens":24},"cost_micros":0}

    id: 29
    event: title
    data: {"chat_id":"4be0c1a2d3e4f506","title":"Suggest three names for a small bakery"}

    id: 30
    event: end
    data: {"id":"9a1f0c2b3d4e5f60","chat_id":"4be0c1a2d3e4f506","status":"done","model":"recorded-turns","user_message_id":41,"reply_message_id":42,"stop_reason":"end","usage":{"input_tokens":24,"output_tokens":52,"thinking_tokens":24},"cost_micros":0,"created":"2026-11-25T09:14:03.120Z","started":"2026-11-25T09:14:03.131Z","ended":"2026-11-25T09:14:03.710Z","last_event_id":30}

If the connection drops after event 14, the client calls

    GET /api/v1/chats/4be0c1a2d3e4f506/turns/9a1f0c2b3d4e5f60/events
    Last-Event-ID: 14

and the stream goes on from event 15.

What a screen does with it:

1. On the first `turn`, note the turn's id and show the stop button.
2. On `message` with the role `user`, put the person's message in the list with its real id.
3. On `thinking` and `text`, add the text to the block at that `step` and `index` of a draft reply.
4. On `message` with the role `assistant`, replace the draft with the stored message.
5. On `title`, set the chat's title in the list.
6. On `error`, show the message. On `end`, hide the stop button.

## Changed since the contract

Nothing yet.
