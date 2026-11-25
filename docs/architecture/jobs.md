# Background tasks

Folders: `internal/jobs`, the turn's side in `internal/turn/jobs.go`, and the screen in `web/app/src/Tasks.tsx`

A background task is work a person or a model starts in a chat that goes on by itself: it outlives the tab and the request that started it, resumes after the server restarts, can be stopped, and writes its result in the chat it was started from. The plan's words: durable jobs that outlive the tab and resume after a restart.

Built in P8-01. Decisions [0290](decisions/0290-a-task-works-through-turns-of-a-chat-of-its-own.md) to [0295](decisions/0295-starting-a-task-asks-and-a-task-starts-no-other.md) say why it is built this way.

## The shape

**A task is not a second runner beside the turns.** It works through ordinary turns ([turns.md](turns.md)) of a chat of its own, its **work chat**, made when it starts and titled "Background task: …". So every model call and tool call a task makes is a turn's: it streams, it is kept in the chat record, it is recorded in the audit log, it asks under the approval policy, and it is stopped the way a turn is. The person can open the work chat and watch every step, answer its approval cards there, or stop it. The chat it was started from stays free for the person to carry on in.

**A task is rounds.** A round is one turn of the work chat, of up to `chats.turn_max_steps` model calls. The first round's message is the task's own words. What follows a round depends on how its turn ended:

| The round's turn | Then |
| --- | --- |
| `done`, the model answered | The task is `done`. Its result is the text of the turn's last reply. |
| `done` at its step limit (`step_limit`) | Another round, with `agents.task_continue_text` as its message, unless the task has taken `agents.task_max_rounds`; then it fails with `round_limit`. |
| `failed` with `interrupted`: the server stopped while it ran | Another round, with `agents.task_resume_text`: the task resumes. The same limit applies. |
| `failed` with any other code | The task fails with the turn's code and message: `spend_cap`, `no_model`, `model_unavailable`, `too_long`, a provider's failure. |
| `stopped` | The task is `stopped`. |

The rounds of a task are the turns of its work chat sent under the task's request id (see "Spend" below), so the task's place is always read from the turns themselves: which round it is on, whether it has one, and how it ended. Nothing a restart could lose is kept in memory.

**When a task ends**, Keystone writes a **note** in the chat it was started from: an assistant message with the stop reason `task`, saying the task is done with its result, or that it was stopped, or why it did not finish. A person who reopens the chat sees it, and the chat's later turns read it like any reply. The note is written by a turn of that chat that calls no model, taken in the chat's queue, so it never lands in the middle of a turn (decision [0292](decisions/0292-the-result-is-a-note-written-in-the-chats-queue.md)).

## Starting one

Two ways, through one function, `Runner.Start`:

- **The person**: `POST /api/v1/chats/{id}/tasks` with `{"text": …, "title": …}`.
- **A model**: the tool `start_background_task`, with `task` and an optional `title`. It changes something (it starts work that runs tools and spends money without the person), so under the approval policy it **asks by default**, and always asks once the chat has read outside content. Its result is Keystone's own words: the task has started, and its result will be written in this chat (decision [0295](decisions/0295-starting-a-task-asks-and-a-task-starts-no-other.md)).

A start is refused when the text is empty, when the chat is a task's own work chat (a task does not start another), and when the person already has `agents.task_open_max` tasks waiting or running. A task runs on the model the chat it was started from would use next, and the test tenant's on its recorded turns.

**A task in its own chat** (P8-03, `internal/jobs/inchat.go`): `Runner.StartIn` starts a task whose work chat is the chat it is started from, as a scheduled task's run is ([schedules.md](schedules.md)). Its first round's message is the task's words, in that chat. Its result is that chat's last reply, so when it is done no note is written and `note_message_id` is the reply; when it is stopped or fails, a note in the chat says so. One task works in a chat: a second `StartIn` returns the first. The same limit of open tasks applies, and the model is the one given, or the chat's own. `jobs.Read` reads one task, and `ErrNoTask`, `ErrTooMany` and `ErrInvalid` are the errors a caller outside the package tells apart. `deliver` and `noteText` in `runner.go` each gained a line for it.

## After a restart

`Runner.Recover` runs once when the server starts, right after the turn runner's, for every tenant. The turn runner has already closed a round that was running as `failed` with `interrupted`, keeping the words it had streamed (decision 0142), and put a round that waited for an approval back in its chat's queue (decision 0222). Recover then starts a goroutine for every task that is waiting or running, and for every task that ended and whose note is not yet written:

- a task whose round was cut short sends its next round, with `agents.task_resume_text`: the model is told it was cut short and to check what was done before doing anything again;
- a task whose round waits for an approval waits again, and goes on when the person answers;
- a task whose note was not written writes it, once (decision [0291](decisions/0291-a-task-resumes-with-a-new-round.md)).

A task's goroutine runs under the turn runner (`turn.Runner.Go`), so closing the turn runner stops it and waits for it, and writes nothing more.

## Approvals

Nothing new. A round's tool that changes something asks as any turn's does: its card is in the work chat, the round waits, and the task reads as waiting (`waiting: true`, "Waiting for your approval to run …") until the person answers. The wait survives a restart as P7-02 built it. Nothing a task does can approve a call ([approvals.md](approvals.md)).

**The marks go with the words** (decision [0294](decisions/0294-a-chats-marks-go-with-its-task-both-ways.md)). A task's text comes from the chat it was started from, and its result goes back there, so:

- a work chat starts marked as having read outside content when the chat it came from had (fixed rule 2: every tool that changes something asks in it, whatever its policy), and held for sensitive content when that chat was (fixed rule 3);
- the chat that receives a task's note is marked the same way when the work chat read outside content or was held.

The marks are written before the first round can run, and before the receiving chat can run another turn: `turn.Runner.Send` and `turn.Runner.Note` hold the chat's queue while they write them.

## Spend

**Every task has a request id of its own**, made when it starts. Every turn of the task is sent under it, so the task's model calls and tool calls read together in the audit log, its usage records carry it, and its spend is their sum (models that are not local only). The API call that started it is kept beside it as `started_request` (decision [0293](decisions/0293-a-task-has-its-own-request-id-and-budget.md)).

**Its own budget.** `agents.task_budget_usd`, a person's setting ($1 as shipped), is kept on the task when it starts. Before each call to a model that is not local, after the monthly cap, the turn runner asks `jobs.Gate`: when the task's spend and the most the call could cost are together more than its budget, the call is not sent. It is the cap's own kind of refusal, `spend_cap`, so the route falls back to a local model when the person's chain has one, and otherwise the round fails and the task ends with the message: "The call to … was not sent. The background task “…” has spent $… of its budget of $…, and this call could cost up to $…". The monthly cap is asked first and still applies to every call.

## The routes

Every path starts with `/api/v1`. `{id}` is a chat's id; `{task}` is a task's id, sixteen hex characters. A task is reached through the chat it was started from or through its work chat.

| Route | Request | Reply |
| --- | --- | --- |
| `GET /chats/{id}/tasks` | | `TasksReply`: `tasks`, the ones started from the chat or working in it, oldest first. |
| `POST /chats/{id}/tasks` | `StartBody`: `text`, and optionally `title`. | `Task`, 201. |
| `GET /chats/{id}/tasks/{task}` | | `Task`. |
| `GET /chats/{id}/tasks/{task}/events` | Header `Last-Event-ID`, or query `after`. | A stream of events, from the start or from after that event. |
| `POST /chats/{id}/tasks/{task}/stop` | No body. | `Task`, as it stands after the stop. |

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | No such chat or task in this person's record. |
| `bad_request` | 400 | An empty task, or a start from a task's own chat. |
| `too_many_tasks` | 429 | The person has `agents.task_open_max` tasks waiting or running. |

### `Task`

| Field | Meaning |
| --- | --- |
| `id`, `chat_id`, `work_chat_id` | The task, the chat it was started from (its note goes there), and its own chat. |
| `title`, `task` | Its name in a few words (the first words of the task when none was given), and the words it was given. |
| `started_by` | `person` or `tool`. |
| `status` | `queued`, `running`, `done`, `stopped` or `failed`. |
| `waiting` | A call of its round waits for the person's approval, in its work chat. |
| `progress` | Where it is, in a few words: "Starting", "Using search_chats", "Writing", "Waiting for your approval to run …", "Resuming after a restart", "Done". |
| `rounds`, `max_rounds`, `steps` | The turns it has sent, the most it may, and the model calls they have finished. |
| `turn_id` | Its round's turn, in the work chat. |
| `budget_micros`, `spent_micros` | Its budget and its spend, in millionths of a US dollar. |
| `result` | The text of its last reply, once it is done. |
| `error` | On a failed task, a `TaskError`: `code` and `message`. The codes are `round_limit`, `chat_deleted`, `failed`, and the code of the turn its last round failed with. |
| `note_message_id` | The note in `chat_id`, once it is written. |
| `created`, `started`, `ended`, `last_event_id` | |

### The stream

| Event | Data | When |
| --- | --- | --- |
| `task` | `Task` | First, and whenever the task changes: a round starts, a step ends, a call waits for the person, the task ends. A client keeps the newest. |
| `end` | `Task` | Always the last, once the note is written in the chat it was started from. The stream closes after it. |

Ids count from 1 within a task with no gaps, and a client that lost its connection sends the last id it saw, as on a turn's stream. The stream says where the task is; the steps themselves stream on the round's turn in the work chat.

### Stop

`POST …/stop` writes the stop on the task, stops its round's turn (which keeps what it had streamed), and waits for the task to end, up to `chats.stop_wait`. Stopping the round's turn in the work chat stops the task too. Deleting the chat a task was started from deletes the task and stops its round; deleting its work chat ends it as `failed` with `chat_deleted`.

## On the screen

A card under the conversation for each task under way, started from the chat or working in it: its title, where it is ("Using search_chats · 1 step done", pulsing while it works, or "Waiting for your approval, in its chat"), a link to the other chat, and **Stop**. It follows the task's stream; when the task ends the chat is read again and the card goes. The note is drawn as a reply of its own, "From a background task", with a link to the task's own chat. A model's `start_background_task` reads "Starting a background task, “title”" in its step row and on its approval card. The code is `web/app/src/Tasks.tsx` and `tasks.css`, with lines in `api.ts`, `Chat.tsx`, `layout.ts`, `Conversation.tsx` and `steps.ts`; [web.md](web.md) has the calls. There is no mockup yet: the look is a first draft from the tokens until the agent mockups (P1-05).

## Tables

In `internal/tenant/migrations/20261125T163000_jobs.sql`.

| Table | Holds |
| --- | --- |
| `jobs` | One row for each task: its two chats, title and words, who started it, its status, its own request id and the one that started it, its actor and model, its budget and spend, its round, steps and words of progress (written from its turns, which are the truth), its result and error, the turn and message of its note, `stop_requested`, the id of its newest event, and three times. It goes with the chat it was started from. |
| `job_events` | One row for each event a task streamed, as `turn_events` holds a turn's. |

## The turn's side

`internal/turn/jobs.go`, a new file, and nothing in it changes how a turn runs:

    turns.Send(ctx, store, chatID, requestID, actor, body, carry)   // a turn with no client; carry's marks first
    turn.Get(ctx, store, chatID, turnID)                             // one turn
    turn.Latest(ctx, store, chatID, requestID)                       // a chat's newest turn sent under a request id, and how many
    turn.Calls(ctx, store, chatID, requestID)                        // the model calls they finished
    turn.Pending(ctx, store, turnID)                                 // the tool a call waits for approval to run
    turn.Events(ctx, store, turnID, after, limit)                    // a turn's events
    turn.ReplyText(store, t)                                         // the text of its last reply
    turns.Stop(ctx, store, chatID, turnID)                           // as the stop route does
    turns.Changed(tenantID, turnID)                                  // closed when the turn has news
    turns.Go(func(ctx))                                              // a goroutine the runner waits for at Close
    turns.ChatModel(ctx, store, chatID)                              // the model a chat's next turn would use
    turns.Carried(ctx, store, chatID)                                // a chat's marks
    turns.Note(ctx, tenantID, chatID, turnID, requestID, actor, blocks, carry) // a note, in the chat's queue

`turn.Options.Gate` is the one new option: a function asked before each call to a model that is not local, after the monthly cap. `internal/app` sets it to `jobs.Gate`. `route.go` changed one line, so that a route's gate is the cap and then the option.

## In code

    runner := jobs.NewRunner(jobs.Options{Install: in, Log: log, Config: cfg, Turns: turnConfig})
    runner.AddTool(registry)          // start_background_task, before the turn runner is made
    turns := turn.NewRunner(turn.Options{..., Tools: registry, Gate: jobs.Gate})
    turns.Recover()
    runner.Bind(turns)                // the first turn runner given stays
    runner.Recover()                  // once, when the server starts, after the turns'
    routes = append(routes, jobs.Routes(runner)...)
    turns.Close()                     // stops the tasks' goroutines too, and writes nothing

`internal/app` does all of this (`newJobs` in `internal/app/jobs.go`, and lines in `app.go` and `tools.go`). A caller that makes its own tool registry, as some checks do, gets no `start_background_task`.

## Settings

In `settings/agents.json`. `jobs.ReadConfig` reads the install's, and `app.ReadConfig` calls it. The budget is read from the person's stored value when a task starts.

| Key | Default | Scope | Meaning |
| --- | --- | --- | --- |
| `agents.task_budget_usd` | 1 | tenant | The most one task may spend on models that are not local. |
| `agents.task_max_rounds` | 6 | install | The most rounds one task may take. |
| `agents.task_open_max` | 3 | install | The most tasks one person may have waiting or running at once. |
| `agents.task_continue_text` | see the file | install | The message a round is sent with after the round before reached the step limit. |
| `agents.task_resume_text` | see the file | install | The message a round is sent with after a restart cut the round before short. |

`chats.turn_max_steps`, `chats.stream_poll`, `chats.stream_batch` and `chats.stop_wait` ([turns.md](turns.md)) are read here too.

## Checked by

- `go test ./internal/jobs`, with a real HTTP server, the turn runner, the fake provider and invented tools:
  - **A task runs to its result**: started by the route, it searches the person's chats in its own chat with no client, its result is a `task` note in the chat it was started from, its model calls and tool call are in the audit log under its own request id, a dropped stream resumes, and another person's session reaches none of it.
  - **A task finishes after a restart**: the first server is stopped in the middle of the round's model call; the second closes the round as interrupted, resumes with `agents.task_resume_text`, and the task ends as done with its result in the chat. This is the item's proof.
  - A round at its step limit is followed by another, and a task with one round fails with `round_limit`. A model starts a task with the tool after the person approves the start, and the result follows in the same chat. A tool that changes something waits for the person, the task says so, and it runs once when approved. A task started from a chat that read outside content asks for a tool set to always, and its note marks the chat it lands in; one started from a clean chat runs it. Stop ends a task and tells the chat. Deleting its chat stops its round. The budget stops a paid call that could pass it, and the task falls back to a local model when the chain has one. An empty task, a start from a work chat and one start too many are refused, and a refused start leaves no chat behind.
- `verify`, the standing check `background tasks` (`tools/verify/jobs.go`): on the real server as the test tenant, a task's round is cut short by stopping the server while its tool runs; the second server resumes it; the task ends as done after two rounds; its result is a `task` note, the last message of the chat it was started from; its rounds are one interrupted and one done; its model calls and the tool are in the audit log, and its model calls in the usage records, under its own request id on both sides of the restart; and another tenant gets 404 from every task route. It was seen to fail when a round cut short by a restart ended the task instead of resuming it.
- The route walks in `verify` call the five routes like every other: `tenant isolation`, `calls recorded` and `no keys in output`.
- In a browser, on port 7971, as the test tenant, as [web.md](web.md) describes: a task started by asking for one, its approval, its note; running and waiting cards; Stop; and a server killed and started again while one task ran and another waited, after which the first finished with its result in the chat and the second waited on and finished once approved.

## Not here yet

- **Unattended spend and the pause switch** (P8-05). A task has its own budget and the monthly cap applies; a budget a person sets for one task, a notice when a task falls back to a local model, and the per-tenant switch that pauses every task and schedule are P8-05's.
- **A list of every task**, across chats, and on Operations (P11-03). The routes are reached through a chat.
- **Retrying a provider's failure.** A round that fails with `overloaded` or `rate_limited` ends the task. A task does not wait and try again.
- **A task's own model.** It runs on the model the chat it was started from would use next.
- **The memory pass's incognito flag** (P6-06, built beside this). A task started from an incognito chat should have an incognito work chat, so its rounds get no memory pass. It is one call in `Runner.Start` once both are merged; the journal, agents 004, has it. A note runs no memory pass.
- **Sub-agents** (P8-02), which run beside a turn and report to it. Scheduled tasks (P8-03) are built: each run is a task in its own chat, as above.
- A tool that ran in a round the stop cut short, and finished, is in the audit log; the model is told to check before doing anything again, and a changing call is never run twice (decision 0220), but a read-only one may be.
