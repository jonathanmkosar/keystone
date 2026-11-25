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
| `GET /chats/{id}/pins` | | `PinsReply`: `chat_id` and `pinned`, the ids of the pinned messages, oldest first. See [compaction.md](compaction.md). |
| `PUT /chats/{id}/pins/{message}` | No body. | `PinsReply`, after pinning the message. |
| `DELETE /chats/{id}/pins/{message}` | | `PinsReply`, after taking the pin off. |
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
| `stop_reason` | On an assistant message: `end`, `tool_call`, `max_tokens` or `refused` from the model, `stopped` when the person stopped it, `failed` when the call broke off, `step_limit` on the note a turn writes when it reaches its step limit. |
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
| `stop_reason` | The model's last stop reason, or `step_limit` for a turn that reached its step limit and ended as `done`. |
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
| `approval` | `Approval` | Since P7-02: a tool call waits for the person's answer, or was answered. See [approvals.md](approvals.md). |
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
| `step_limit` | Not sent since the tool loop (P7-01): a turn at `chats.turn_max_steps` ends as `done` with a note. A turn an earlier build ended may still hold it. |
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
    data: {"chat_id":"4be0c1a2d3e4f506","title":"Suggest three names for a small"}

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

No route, request, reply or event changed shape between the contract commit and the build. `docs/api/openapi.json` is the same file in both. Three things are worth knowing that the contract did not say:

- The `not_built` code is no longer answered by any route.
- The title in the worked example was one word too long. The fake provider writes no title, so the test tenant's titles are the first `chats.title_derived_words` words of the message, six as shipped.
- A turn's `usage` and `cost_micros` count its chat calls. The title call is recorded in the audit log and is not added to the turn.

Since the tool loop (P7-01), one value was added and one code retired. No route, request, reply or event changed shape, and `docs/api/openapi.json` was regenerated and is the same file, because it lists `stop_reason` as a string:

- `stop_reason` on a `Message` and on a `Turn` may be `step_limit`. The message is the note a turn writes when its last allowed model call still asks for tools, with the text of `agents.step_limit_note`. The turn then ends as `done` with the same stop reason (decision [0202](decisions/0202-the-step-limit-ends-a-turn-with-a-note.md)).
- The `error` code `step_limit` is no longer sent.
- `tool_result` now carries a tool's real result. It is an error result (`is_error: true`) when the tool failed, broke, ran too long, does not exist, changes something and has no approval, was declined at the step limit or with tools switched off, or was stopped. `tool_call` and `tool_result` are as they were.

Since the approval policy (P7-02), added and nothing changed. `docs/api/openapi.json` was regenerated: it gains the routes and the event below, and every path and shape it had is the same. The whole of it is [approvals.md](approvals.md).

- A stream event, `approval`, with an `Approval`: a tool call that needs the person's answer before it runs (`status` `pending`), sent again when it is answered (`approved`, `declined`, `expired` or `stopped`). A client keeps the newest by `id`. It comes after the `usage` event of the step whose calls ask, and before their `tool_result` events. A turn that waits stays `running`, and its stream stays open, with heartbeats.
- `GET /chats/{id}/turns/{turn}/approvals`: `ApprovalsReply`, the turn's approvals.
- `POST /chats/{id}/turns/{turn}/approvals/{approval}/approve` and `/decline`: the person's answer to one call, with no body. 404 `not_found`, 409 `answered` or `turn_ended`.
- `GET /tools`, `PUT /tools/{name}/policy` and `DELETE /tools/{name}/policy`: each tool with the person's policy.
- New sentences in `tool_result` for a call that was not run: declined by the person, not answered in time, stopped while it waited, set to never, or an approval already used.
- A turn that was running when the server stopped, waiting for an answer, is put back in the queue and waits again, instead of being closed as `interrupted`.

Since the turn inspector (P5-04), added and nothing changed. `docs/api/openapi.json` was regenerated: it gains the four routes of `InspectorRoutes` and their shapes, and every path and shape it had is the same. The whole of it is [inspector.md](inspector.md).

- `GET /chats/{id}/turns/{turn}/inspect`: `InspectReply`, what went into each model call of the turn (the context builder's record of every part with its tokens, and the first words of each turn), each call's usage records and their total, the turn's recall record, the summaries its calls were given, the totals of every usage record of the turn, the chat's pins, and what the next turn leaves out.
- `GET /chats/{id}/skips`: `SkipsReply`, what the chat's next turn leaves out.
- `PUT /chats/{id}/skips/{kind}/{ref}` and `DELETE` on the same path, with no body: take a recalled page or source (`kind` `page` or `source`, `ref` its id in the knowledge store) or a pinned message (`kind` `message`) out of the chat's next turn only, or put it back. 400 `bad_request` for a kind or id that is not one, or a message that is not pinned; 404 `not_found` for a page or source no recent call of the chat was given.
- Each model call of a turn now leaves out what the turn claimed: `build` in `context.go` calls `leaveOut` in `inspect.go` (decision [0271](decisions/0271-a-skip-is-used-by-the-next-turn-and-no-other.md)).

Since background tasks (P8-01), added and nothing changed. `docs/api/openapi.json` was regenerated: it gains the five task routes and their shapes, and every path and shape it had is the same. The whole of it is [jobs.md](jobs.md).

- `stop_reason` on a `Message` and on a `Turn` may be `task`: the note a background task writes in the chat it was started from when it ends, as an assistant message. The turn that writes it calls no model: it is written `running` with the input `{}`, gets its `turn`, `message` and `end` events, and ends as `done`. It is taken in the chat's queue, so it never lands in the middle of a turn.
- A chat may have turns no client sent: a background task's rounds, sent under the task's own request id, and the note. They stream and record like any turn, and a screen that opens the chat joins one that is running as before.
- `spend_cap` may come from a background task's budget as well as the monthly cap, with a message that names the task.
- Five task routes under `/chats/{id}/tasks`, in `internal/jobs`, added to the one list by `internal/app/app.go`. The tenant isolation, calls recorded and no keys in output walks call them like every other.
- One option, `Options.Gate`, asked before each call to a model that is not local, after the monthly cap. `route.go` changed one line, so a route's gate is the cap and then the option. And one new file, `jobs.go`, of what tasks use: `Send` (a turn with no client), `Get`, `Latest`, `Calls`, `Pending`, `Events`, `ReplyText`, `Stop`, `Changed`, `Go`, `ChatModel`, `Carried` and `Note`. Nothing in it changes how a turn runs.

Since connectors (P7-04), added and nothing changed. `docs/api/openapi.json` was regenerated: it gains the seven routes of `connectors.Routes` and their shapes, and every path and shape it had is the same. The whole of it is [connectors.md](connectors.md).

- `GET /connectors`, `POST /connectors`, `PUT /connectors/{name}`, `DELETE /connectors/{name}` and `POST /connectors/{name}/check`: the person's connectors. 403 `command_line_only` for a command sent through the API, 400 `bad_connector`, 409 `exists` or `full`, 404 `not_found`.
- `GET /chats/{id}/connectors` and `PUT /chats/{id}/connectors/{name}` (`{"on": bool}`): the chat's tools menu. 404 `not_found` for a chat or a connector that is not in the person's record.
- A turn may now call `load_tools` and a connector's tools, `<connector>_<tool>`, which stream as `tool_call` and `tool_result` as every tool does. `GET /tools` lists the connector tools the person's connectors last listed, and a policy can be set for each.

Since scheduled tasks (P8-03), added and nothing changed. `docs/api/openapi.json` was regenerated: it gains the ten schedule routes and their shapes, and every path and shape it had is the same. Nothing in `internal/turn` changed. The whole of it is [schedules.md](schedules.md).

- A chat may be a scheduled task's run: made by the scheduler with a title (the schedule's name and the date), its first turn sent by a background task under the task's own request id, with the schedule's prompt as the first message. Its turns stream and record like any turn, with the actor `schedule` in the audit log for a scheduled run and `person` for a run now. Its last reply is the task's result, with no note; a note with the stop reason `task` follows only when the run was stopped or failed.
- `GET /chats/{id}/tasks` on a run's chat lists the run's task, whose `chat_id` and `work_chat_id` are both that chat.
- Ten schedule routes under `/schedules`, in `internal/schedules`, added to the one list by `internal/app/app.go`. The tenant isolation, calls recorded and no keys in output walks call them like every other.

## How a turn runs

Decisions [0140](decisions/0140-a-turn-and-its-events-are-rows.md) to [0144](decisions/0144-the-test-tenants-model-is-in-no-registry.md) say why it is built this way.

**A turn is a row, and so is each event.** `POST /chats/{id}/turns` checks the body, writes a row in `turns` with the status `queued` and the body it was sent with, writes the first event, and starts reading the turn's events back to the client. Nothing else happens in the request. The request can end at any point and the turn is not affected.

**The runner works each chat's turns one at a time.** `turn.Runner` starts one goroutine for a chat that has a turn waiting. The goroutine opens a store of its own for the tenant, takes the chat's oldest queued turn, runs it to its end, and takes the next. When none waits it closes its store and ends. Turns in different chats, and different tenants, run side by side. A turn sent while another runs in the same chat waits behind it, up to `chats.turn_queue_max` waiting.

**One turn**, in `Runner.work`:

1. The model is opened. See "Which model" below. A failure here ends the turn with an `error` event, and nothing is written to the chat.
2. The person's message is appended to the chat record with `chat.Append`, under the last message of the current branch. With `edit_of` it goes under the edited message's parent. With `retry_of` nothing is appended, and the reply goes under the retried reply's parent.
3. The request is built by the context builder ([context.md](context.md)) from the branch that ends at that message: the standing instructions from `chats.context_system_text`, the current turn, and as many earlier turns as fit the model's window, with `chats.turn_max_output_tokens` and `chats.turn_thinking_tokens`. What the call was built from is stored in `turn_contexts`.
4. The provider's `Stream` is called. Each seam event goes through a `seam.Collector` and is written as a row in `turn_events`: text and thinking as they arrive, a tool call when it is whole.
5. The reply is appended to the record, with the model, the stop reason, the usage and the cost in `turn_messages`.
6. If the model stopped for a tool, each call is run, the results are appended as a message, and step 3 runs again, up to `chats.turn_max_steps` model calls. At the limit the last calls are answered without running and the turn ends as `done` with a note. The tool loop is [tool-loop.md](tool-loop.md).
7. A chat with no title gets one.
8. The turn is closed with `end`.

**The request holds what a provider takes.** The record holds what happened, and that includes replies that were stopped or broke off. `buildMessages` leaves out a thinking block that has no signature or was written by another model, a tool call that has no result in the next message, and a result whose call was left out. A message left with nothing is dropped, and two messages in a row from the same side become one.

**A reader follows the table.** A stream reads the turn's rows in `turn_events` after the id it was given and sends them. When there are none it waits to be woken by the runner, or for `chats.stream_poll`, and reads again. So `Last-Event-ID` needs no code of its own, a turn can be read after it has ended, and it does not matter which start of the server wrote the events.

### Which model

`GET /chats/{id}/model` and a turn that names no model use the same order:

1. The model the chat's last turn used, if the person still has it.
2. The person's `chats.default_model`, if it names a model they have.
3. The first model in their registry, by name.

Only models that pass the registry's check today, whose adapter this build has, and that are not embedding models count. With none, the turn fails with `no_model` and the reason.

A turn that names a `model` gets that one, or fails with `model_unavailable`.

The provider comes from `adapters.Open` with the set in `app.Config.Adapters`, and is wrapped in `budget.Guard`, so a chat that does not fit the model's window ends as `too_long` before it is sent ([budget.md](budget.md)).

**The test tenant gets its recorded turns and nothing else.** When `testtenant.IsTest` is true for the store, the registry is not read and the turn's `model` field is ignored. The model is `recorded-turns`, the provider is `testtenant.Provider`, the fake playing the fixtures' scripts, behind the same guard. Decision [0144](decisions/0144-the-test-tenants-model-is-in-no-registry.md).

### Stop

`POST .../stop` writes `stop_requested` on the turn's row, then:

- A turn that waits is closed as `stopped` at once.
- A turn that runs has its context cancelled, which is how the seam says a call is stopped. The provider returns, the runner appends what `Collector.Partial` holds as an assistant message with the stop reason `stopped`, and closes the turn. The route waits for that, up to `chats.stop_wait`, and answers with the turn.
- A turn that has ended is answered as it is.

Deleting a chat stops its turns first.

### After a restart

`Runner.Recover` runs once when the handler is made, for every tenant. Decision [0142](decisions/0142-an-interrupted-turn-fails-and-a-queued-one-runs.md).

- A turn that was **queued** runs. Its body is in its row, and the request id it was sent under still names its model call in the audit log.
- A turn that was **running** is closed as `failed` with the code `interrupted`. The text it had streamed and not yet stored is read back from `turn_events` and appended as an assistant message with the stop reason `failed`, so the chat shows what the person saw.

`Runner.Close` stops the goroutines and writes nothing, which leaves the rows exactly as a killed server would. `app.Open` returns it beside the handler, for a caller that removes the data folder afterwards.

### Titles

After a reply, while the chat has no title and `chats.title_auto` is on:

- A model writes it: the one `chats.title_model` names, or the model that answered. It is sent `chats.title_prompt` as its standing instructions and the first `chats.title_input_chars` characters of the message and of the reply. The answer's first line is used, without quotation marks or a full stop, cut to `chats.title_max_length`. The call is checked against the window and recorded like any other.
- The fake provider writes none, and neither does a call that fails or takes longer than `chats.title_timeout`. The title is then the first `chats.title_derived_words` words of the person's message.

A title the person gave is never replaced: the title is looked at again just before it is set.

### The audit log

Each model call leaves two entries of the kind `model` in the tenant's log, with the request id of the `POST .../turns` call that sent the turn and the model's name as the action ([audit.md](audit.md)).

| Entry | Outcome | Holds |
| --- | --- | --- |
| Before the call | `started` | `sent`: the seam request. |
| A whole reply | `ok` | `received`: the seam response. The usage, the cost and the time taken. |
| Stopped by the person | `ok` | `received`: `{"stopped": true, "partial": …}`. |
| Refused by the budget | `refused` | `received`: the seam error, `too_long`. |
| Any other failure | `error` | `received`: the seam error. |

A call whose first entry cannot be written is not made. A reply whose end cannot be recorded is not used. The model's key is read from the vault by name and passed to `audit.Append`, so it is removed from anything recorded. A call cut off by the server stopping has a `started` entry with nothing after it.

## Tables

In `internal/tenant/migrations/20261125T030000_turns.sql`.

| Table | Holds |
| --- | --- |
| `turns` | One row for each turn: its chat, status, the body it was sent with, the request id and actor, the model, the two message ids, the stop reason, the error, the summed usage and cost, `stop_requested`, the id of its newest event, and three times. |
| `turn_events` | One row for each event: the turn, the event's id, its name and its JSON. |
| `turn_messages` | What a turn recorded about a message it wrote: the turn, and for a reply the model, the stop reason, the usage and the cost. |

All three name their chat or turn with `ON DELETE CASCADE`, so `chat.Delete` removes them with no change to the chat record.

## Settings

In `settings/chats.json`. `turn.ReadConfig` reads the install's, and `app.ReadConfig` calls it. The ones a person may set are read from their stored values for each turn.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `chats.default_model` | empty | tenant | The model a new chat uses. Empty means the first usable one. |
| `chats.turn_queue_max` | 5 | install | The most turns that may wait in one chat. |
| `chats.turn_max_steps` | 8 | install | The most model calls one turn may make. |
| `chats.turn_max_output_tokens` | 0 | tenant | The most tokens a reply may hold. Zero means the model's own limit. |
| `chats.turn_thinking_tokens` | 0 | tenant | The most tokens spent on thinking. Zero asks for none. Sent only to a model with the `thinking` capability. |
| `chats.stream_poll` | 1s | install | How often a stream looks for events when nothing wakes it. |
| `chats.stream_batch` | 200 | install | The most events a stream reads at a time. |
| `chats.stop_wait` | 2s | install | How long the stop route waits for the turn to end. |
| `chats.title_auto` | true | tenant | Whether a chat gets a title after its first reply. |
| `chats.title_model` | empty | tenant | The model that writes titles. Empty means the one that answered. |
| `chats.title_prompt` | see the file | install | The instruction the title model is given. |
| `chats.title_max_tokens` | 40 | install | The most tokens of a title. |
| `chats.title_input_chars` | 2000 | install | How much of the message and the reply the title model sees. |
| `chats.title_timeout` | 20s | install | How long the title model may take. |
| `chats.title_derived_words` | 6 | install | How many words of the message make a title with no model. |
| `chats.test_model_context_window` | 200000 | install | The window the test tenant's turns are checked against. |
| `chats.test_model_output_limit` | 8192 | install | The output limit of the test tenant's model. |

The three chat record settings in [chat.md](chat.md) and the budget's in [budget.md](budget.md) are read here too.

## In code

    runner := turn.NewRunner(turn.Options{Install: install, Config: config, PageLimit: 200, Adapters: set})
    runner.Recover()                 // once, when the server starts
    routes := turn.Routes(runner)    // added to the list in internal/app
    runner.Close()                   // stops its goroutines and writes nothing

`app.Config.Adapters` is the set of adapter builders. It is nil as shipped, which means the fake alone. Each real adapter (P3-04 to P3-06) is added to the set by whoever starts the server, one line each in `cmd/keystone/serve.go`.

## Checked by

- `go test ./internal/turn`, with a real HTTP server from `httptest` and the fake provider.
  - **A turn streams to a client**: the events arrive in order with ids from 1, the deltas join to the scripted reply, and the record, the title, the usage and the two audit entries are as the stream said. A dropped connection resumes after `Last-Event-ID`, and after `after`. A turn with a tool call makes its second model call. A retry makes a branch. The list, search, rename, star and delete routes work, and another person's session reaches none of it.
  - **Stop ends the model call**: a provider that waits in the middle of a reply sees its context end, the half reply is in the record as `stopped`, and the stream closes with `end`.
  - **A queued turn finishes after a restart**: a server is stopped with one turn in its model call and one waiting. A second server on the same folder runs the waiting turn to `done` and closes the other as `interrupted` with its words kept.
  - A tenant with no model gets `no_model` and no model call. A registry model is used. A model with no adapter is `model_unavailable`. A chat over the window is `too_long`. A model that is not the fake writes the title, and both calls are recorded with their cost. A branch with a stopped tool call and another model's thinking becomes a request the seam accepts.
- The route walks in `verify` call the twelve routes like every other: `tenant isolation`, `calls recorded` and `no keys in output` ([tools.md](tools.md)). Since P7-02 they call the six approval routes of `ApprovalRoutes` the same way.
- `calls recorded` also sends one message as the test tenant with the shipped settings and reads the audit log: the API call and the model call are each there before and after, under one request id, the model is `recorded-turns`, and the other tenant's log is empty. The code is in `tools/verify/turns.go`.
- The tool loop's tests and checks, the test tenant's task of several steps among them, are in [tool-loop.md](tool-loop.md).

## Not here yet

- Approvals are built (P7-02): a call that asks pauses the turn until the person answers ([approvals.md](approvals.md)).
- Caching (P5-03). Since P5-02 the oldest turns of a long chat are summarised and the summary sent in their place (see "Compaction" below). A single message larger than the window still fails with `too_long`. Since P5-03 each call keeps its start the same from turn to turn and carries cache points the Anthropic adapter marks; the runner's code did not change for it ([caching.md](caching.md)).
- Uploads (P9-02). `blocks` takes image and file blocks, and the files behind a `ref` are not yet read by anything.
- Removing old rows from `turn_events`. Every delta is a row and none is removed, except with its chat.
- A turn left `running` by a second server on the same data folder is not noticed. One server for each data folder is assumed.
- A title call that times out is recorded with the outcome a stopped call has.
- A way to change which model a chat uses, other than naming one in the next turn, and routes for the person's settings (P11-01).

## Connectors

Since P7-04, `build` in `context.go` asks `withConnectors` in `connectors.go` for what the call offers from the person's connectors, after the server's tools and the knowledge lookups, and keeps on the turn's row (`t.connectors`, made in `work`) every connector tool the call's replies may name. `toolFor` and `runPlanned` in `approvals.go` find those tools there, run each through a registry of its own, and `noteOutside` marks the chat once one that is not inside has run. A paused step asks for them again in `resumeStep`. `policies.go` lists them for the approvals screen. `Options.Connectors` is the server's `connectors.Pool`, closed with the runner. See [connectors.md](connectors.md).

## Routing, usage and the spend cap

Since P3-08 a turn does not open one model. `open` asks `internal/turn/route.go` for the route of the model picked: that model first, then the person's fallback chain, as one provider ([routing.md](routing.md)). `call` then asks which model the call ended on, records the call's end under that model, and writes its usage record ([usage.md](usage.md)).

- The turn's `model` is the one asked for. A reply written by a fallback has the fallback's name in its own `model`, and so does the turn's `usage` event.
- When a model of the route did not answer and the next one did, the audit log holds a started entry and an end entry for each.
- Two more codes can end a turn: `spend_cap`, when the call could take the month past the cap and was not sent, and `sensitive`, when the turn holds sensitive content and the person has no local or trusted model. Recall (P6-05) sets the second ([knowledge-recall.md](knowledge-recall.md)).

## Recall

Since P6-05, `work` searches the person's knowledge store once, before it opens the route, with `searchKnowledge` in `internal/turn/recall.go`, and keeps what was found on the turn's row. `routed` asks whether the call is sensitive: the chat was sent sensitive content before, or the chat's model may read it and recall kept some. `build` makes each call for the least of the route's models, the person map included, fills the context builder's `recall` slot, adds the search-first rule to the standing instructions when a store is open, and keeps the record of the first call in `turn_recall`. A store that is missing, down or slow gives nothing and one line in the log, within `knowledge.recall_timeout`. See [knowledge-recall.md](knowledge-recall.md).
- A title is routed as its own kind of task: the model `chats.title_model` names, then the chat's own.

## Compaction

Since P5-02, `build` in `context.go` asks `olderHistory` in `compact.go` for the chat's summary that stands for the start of the branch, and for the pinned messages on it, and gives both to the context builder. After the call is built it calls `compactLater`, which starts a summary of the oldest turns in the background when the call's record says the chat has outgrown its room. The summary call goes through the route for the `summary` task, is recorded in the audit log under the turn's request id, and has a usage record of the kind `summary`. A turn never waits for it, and a failure leaves one line in the log. Three routes pin and unpin messages. See [compaction.md](compaction.md).

## The memory pass

Since P6-06, `runTurn` calls `memoryLater` in `memory.go` once it has closed the turn. A turn that ended as `done`, for a person with a knowledge store and the memory pass on, in a chat that is not incognito, queues its pass in `memory_passes`, and the chat's memory worker reads it beside the turn: the person's message and the reply go to the route for the `memory` task, whose gate is the memory pass's reserve and then the cap, under the turn's request id, with a usage record of the kind `memory`, and the facts it answers with are added to the person's store as one source that points back to the chat. A turn never waits for it. Three routes read a chat's passes and set its incognito flag: `GET /chats/{id}/memory`, `PUT` and `DELETE /chats/{id}/incognito`, listed in `internal/app` from `turn.MemoryRoutes`. See [memory-pass.md](memory-pass.md).

## Chat recaps and the past-chat search

Since P5-05, `runTurn` calls `recapLater` in `recap.go` after `memoryLater`. A turn that ended as `done`, in a chat that has no recap or one `chats.recap_after_turns` turns old, starts a wait of `chats.recap_after_idle`, which a later turn's end moves on; then the chat's recap, a short summary, its topics and its decisions, is made by the route for the `summary` task, under the turn's request id, with a usage record of the kind `summary`. A turn never waits for it. Two routes read a chat's recap and make it now: `GET` and `POST /chats/{id}/recap`, listed in `internal/app` from `turn.RecapRoutes`, and `GET /chats` gives each chat's `summary`. Each model call also makes `search_chats` and `read_chat` for the least of its route (`pastchats.go`, from `lookupTools`), which the calls the model makes in reply run in place of the registry's. See [chat-summaries.md](chat-summaries.md).
