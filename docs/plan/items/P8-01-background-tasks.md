# P8-01. Background tasks

Status: done
Lane: agents
Depends on: P7-01
Owner gate: none
Scope: Durable jobs that outlive the tab, resume after restart, and report progress to the chat.
Proof: A background task finishes after a server restart and its result appears in the chat.

See `docs/plan/PLAN.md` for the design this item delivers, and `docs/plan/PARALLEL.md` for lanes and gates.

## Plan and result

### Plan

**The shape.** A background task is a job that works through ordinary turns. It is not a second runner beside the turns: every model call and tool call it makes is a turn's, so it streams, records, asks for approvals, survives a restart and is stopped the way a turn is.

- A task has a chat of its own, its **work chat**, made when it starts and titled "Background task: …". Its first turn's message is the task; the person can open the work chat and watch every step, and answer its approval cards there. The chat it was started from stays free.
- A task is **rounds**: each round is one turn of up to `chats.turn_max_steps` model calls. A round that ends at its step limit is followed by another, with `agents.task_continue_text` as its message, up to `agents.task_max_rounds`. A round the server's stop cut short (`interrupted`, decision 0142) is followed by one with `agents.task_resume_text`: the task resumes after a restart instead of failing. A round that ends with the model's answer finishes the task.
- When the task ends (done, failed or stopped), Keystone writes its result as a note in the chat it was started from: an assistant message with the stop reason `task`, written by a turn of that chat that calls no model, taken in the chat's queue so it never lands in the middle of a turn. A person who reopens the chat sees it, and later turns read it.
- **Fixed rules carry across.** A work chat starts marked as having read outside content when the chat it came from had, and held for sensitive content when that chat was; the note marks and holds the chat it lands in the same way when the work chat read outside content or was held. Marks are written before the first round can run, and before the note's chat can run another turn.
- **Budget.** Every task has its own request id, so its model calls and tool calls read together in the audit log and the usage records. `agents.task_budget_usd` (a person's setting, $1 as shipped) is kept on the task; a call that could take the task past it is not sent, through the same gate as the monthly cap, so the chain falls back to a local model when it has one and otherwise the round fails with `spend_cap` and the task ends saying so. The monthly cap still applies first.
- **Approvals.** Nothing new: a round's changing tool asks as any turn's does, its card is in the work chat, the task waits (shown as waiting on the task), and the wait survives a restart as P7-02 built it.
- **Stop.** The stop route stops the current round's turn and ends the task as stopped. Stopping the round's turn in the work chat does the same. Deleting either chat stops the task.

**The contract.** No existing route, request, reply or event changes shape. Added:

- `GET /chats/{id}/tasks`: the tasks started from the chat or working in it. `POST /chats/{id}/tasks` (`{"text", "title"?}`): start one, 201 with the `Task`. `GET /chats/{id}/tasks/{task}`, `GET /chats/{id}/tasks/{task}/events` (a stream of `task` events, each the whole `Task`, then `end`), `POST /chats/{id}/tasks/{task}/stop`.
- A tool, `start_background_task` (`title`, `task`), which changes something (it starts work that spends money), so it asks by default; its result is Keystone's own.
- A message stop reason, `task`, on the note.

**Server side.** A new package `internal/jobs` (task rows, the runner, the routes, the tool, the gate), one migration `20261125T151700_jobs.sql` (`jobs`, `job_events`, `job_turns`), settings in `settings/agents.json`, and in `internal/turn` one new file (`jobs.go`: send a turn without a client, read and stop one, wait for its news, run a goroutine the runner waits for, the note, the marks) with one line changed in `route.go` (the gate) and one field added to `Options`. Lines added in `internal/app` to make the jobs runner beside the turn runner, recover it after turns, add its routes and the tool.

**Proof.**

- `go test ./internal/jobs`: a task started by the route runs to its result and the note is in the chat; a task started by the tool, after the person approves the start; **a task whose round was cut short by a server stop resumes on the next server, finishes, and its result is in the chat**; a round at its step limit is followed by another; a changing tool in a task asks and does not run until approved; stop; the budget stops a paid call; another person reaches none of it; a chat that read outside content gives its task chat the mark.
- `verify`: a standing check, `background tasks`, on the real server as the test tenant with recorded turns: a task's round is cut short by stopping the server, the second server resumes it, the task ends as done, its result is a `task` note in the chat it was started from, the task's calls are in the audit log under its request id, and another tenant cannot read or stop it. The route walks call the five routes like every other.

**The screen.** A task card in the chat (new files `web/app/src/Tasks.tsx` and `tasks.css`): the title, where it is ("Step 5 · Searching your chats", "Waiting for your approval"), a link to its work chat, and Stop; it follows the task's stream, and when the task ends the chat is read again and the note is drawn as a reply of its own with the task's mark. Calls added at the end of `api.ts`, one line in `Chat.tsx`, a branch in `layout.ts` and `Conversation.tsx`.

**What is left**, for a session that picks this up (updated at each commit):

- Done: the server side. `internal/jobs` (rows, runner, routes, tool, gate) and its tests, the migration `20261125T163000_jobs.sql` (planned as `151700`, re-stamped after the turn inspector's `152500` landed on `reboot`; `jobs` and `job_events`: the rounds are found from the turns by the task's request id, so `job_turns` was not needed), `internal/turn/jobs.go`, the one line in `route.go` and the `Gate` field in `config.go`, the lines in `internal/app`, five settings, the fixture `turns/background-task.json`, the `background tasks` check in `tools/verify/jobs.go`, `docs/api/openapi.json`, `docs/architecture/jobs.md` and decisions 0290 to 0295.
- Done: the screen (`Tasks.tsx`, `tasks.css`, calls added at the end of `api.ts`, a line in `Chat.tsx`, a branch in `layout.ts` and `Conversation.tsx`, a row in `steps.ts`, `web/dist/` rebuilt), seen in a browser on port 7971.
- Done: the lines in `turns.md`, `web.md`, `tools.md`, `test-tenant.md`, `approvals.md`, `usage.md` and `internal/README.md`, the screen's section of `jobs.md`, the journal agents 004, and the status. Nothing is left.

### Result

Done on November 25, 2026. The item has no owner gate, and the proof passes.

- **Durable jobs that outlive the tab.** A background task is a row of its own that works through ordinary turns of a chat of its own, with no client: started by the person (`POST /api/v1/chats/{id}/tasks`) or by a model (`start_background_task`, which asks first). Its rounds stream, record and ask for approvals like any turn; it can be stopped from either chat; it has a budget of its own (`agents.task_budget_usd`), enforced before each paid call through the same gate as the monthly cap.
- **Resume after restart.** A round the stop cut short is closed as interrupted by the turn runner, as before, and the task resumes with a new round that tells the model it was cut short. A round that waited for an approval waits again. The note is written once.
- **Progress to the chat.** A task's stream sends the task whenever it changes, a card in the chat shows where it is and links to its own chat, and when it ends its result is a note in the chat it was started from, drawn as a reply of its own. A chat's marks for outside and sensitive content go with the task both ways.
- **The proof**, in `go test ./internal/jobs` (`TestATaskFinishesAfterARestart`) and in the new standing check `background tasks` on the real server as the test tenant: a task whose round was cut short by stopping the server finishes on the next server, and its result is the last message of the chat it was started from. `verify` is 20 of 20. Also seen in a browser on port 7971, with the server killed and started again.
- The contract: added only. Five routes, one tool, a stop reason (`task`), `docs/api/openapi.json` regenerated, listed in `turns.md` under "Changed since the contract". The doc is `docs/architecture/jobs.md`; decisions 0290 to 0295; the journal is agents 004. The card and the note have no mockup and are a first draft from the tokens until the agent mockups (P1-05).

